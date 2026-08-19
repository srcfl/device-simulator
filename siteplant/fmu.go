//go:build cgo && linux && amd64

package siteplant

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"github.com/srcfl/device-simulator/internal/plant"
)

/*
#cgo CFLAGS: -I${SRCDIR}
#cgo LDFLAGS: -ldl -lm
#include <stdlib.h>
#include "fmi2.h"

void* siteplant_dlopen(const char* path);
const char* siteplant_dlerror(void);
void siteplant_dlclose(void* h);
fmi2Component siteplant_instantiate(void* h, const char* name, const char* guid, const char* resources);
void siteplant_free_instance(void* h, fmi2Component c);
void siteplant_terminate(void* h, fmi2Component c);
fmi2Status siteplant_setup(void* h, fmi2Component c, double start);
fmi2Status siteplant_enter_init(void* h, fmi2Component c);
fmi2Status siteplant_exit_init(void* h, fmi2Component c);
fmi2Status siteplant_dostep(void* h, fmi2Component c, double t, double dt);
fmi2Status siteplant_set_real(void* h, fmi2Component c, const fmi2ValueReference vr[], size_t n, const fmi2Real v[]);
fmi2Status siteplant_get_real(void* h, fmi2Component c, const fmi2ValueReference vr[], size_t n, fmi2Real v[]);
*/
import "C"

type fmuModelDesc struct {
	XMLName xml.Name `xml:"fmiModelDescription"`
	Guid    string   `xml:"guid,attr"`
	CoSim   struct {
		ModelIdentifier string `xml:"modelIdentifier,attr"`
	} `xml:"CoSimulation"`
	Vars struct {
		Scalar []struct {
			Name      string `xml:"name,attr"`
			VR        uint32 `xml:"valueReference,attr"`
			Causality string `xml:"causality,attr"`
		} `xml:"ScalarVariable"`
	} `xml:"ModelVariables"`
}

// FMU is an FMI 2.0 co-simulation slave compiled from SiteEnergy.Site4.
type FMU struct {
	dir   string
	h     unsafe.Pointer
	c     C.fmi2Component
	vr    map[string]uint32
	slots []string
	units map[string]*plant.Plant
	t     float64
	ready bool
}

func (f *FMU) Backend() string { return "fmu" }

func (f *FMU) Attach(key string, unit *plant.Plant) {
	if f.units == nil {
		f.units = map[string]*plant.Plant{}
	}
	if _, ok := f.units[key]; !ok {
		if len(f.slots) >= MaxUnits {
			return
		}
		f.slots = append(f.slots, key)
	}
	f.units[key] = unit
}

// LoadFMU opens a Site4 FMU as a plant.Model.
func LoadFMU(path string) (plant.Model, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "siteplant-fmu-*")
	if err != nil {
		return nil, err
	}
	if err := unzip(abs, dir); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	desc, err := os.ReadFile(filepath.Join(dir, "modelDescription.xml"))
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	var md fmuModelDesc
	if err := xml.Unmarshal(desc, &md); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	if md.CoSim.ModelIdentifier == "" {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("fmu: missing CoSimulation modelIdentifier")
	}
	so := filepath.Join(dir, "binaries", "linux64", md.CoSim.ModelIdentifier+".so")
	cpath := C.CString(so)
	h := C.siteplant_dlopen(cpath)
	C.free(unsafe.Pointer(cpath))
	if h == nil {
		errStr := C.GoString(C.siteplant_dlerror())
		os.RemoveAll(dir)
		return nil, fmt.Errorf("dlopen %s: %s", so, errStr)
	}
	resDir := filepath.Join(dir, "resources")
	_ = os.MkdirAll(resDir, 0o755)
	res := "file://" + filepath.ToSlash(resDir) + "/"
	cname := C.CString("siteplant")
	cguid := C.CString(md.Guid)
	cres := C.CString(res)
	comp := C.siteplant_instantiate(h, cname, cguid, cres)
	C.free(unsafe.Pointer(cname))
	C.free(unsafe.Pointer(cguid))
	C.free(unsafe.Pointer(cres))
	if comp == nil {
		C.siteplant_dlclose(h)
		os.RemoveAll(dir)
		return nil, fmt.Errorf("fmi2Instantiate failed (guid %s)", md.Guid)
	}
	if st := C.siteplant_setup(h, comp, 0); st != C.fmi2OK {
		C.siteplant_terminate(h, comp)
		C.siteplant_free_instance(h, comp)
		C.siteplant_dlclose(h)
		os.RemoveAll(dir)
		return nil, fmt.Errorf("fmi2SetupExperiment status %d", int(st))
	}
	f := &FMU{
		dir:   dir,
		h:     h,
		c:     comp,
		vr:    map[string]uint32{},
		units: map[string]*plant.Plant{},
	}
	for _, sv := range md.Vars.Scalar {
		f.vr[sv.Name] = sv.VR
	}
	for _, name := range []string{"ghi", "P_load", "P_meter", "u_on[1]", "P_pv[1]"} {
		if _, ok := f.vr[name]; !ok {
			f.close()
			return nil, fmt.Errorf("fmu: %s is not a SiteEnergy.Site4 FMU (missing %s)", path, name)
		}
	}
	runtime.SetFinalizer(f, (*FMU).close)
	return f, nil
}

// Close releases the FMU instance and unpacked files.
func (f *FMU) Close() {
	runtime.SetFinalizer(f, nil)
	f.close()
}

func (f *FMU) close() {
	if f.h != nil && f.c != nil {
		C.siteplant_terminate(f.h, f.c)
		C.siteplant_free_instance(f.h, f.c)
		f.c = nil
	}
	if f.h != nil {
		C.siteplant_dlclose(f.h)
		f.h = nil
	}
	if f.dir != "" {
		os.RemoveAll(f.dir)
		f.dir = ""
	}
}

func (f *FMU) StepSite(dt float64, in plant.SiteStep) (plant.SiteResult, error) {
	if dt < 0 {
		dt = 0
	}
	if dt > 6*3600 {
		dt = 6 * 3600
	}
	for _, m := range in.Members {
		if _, ok := f.units[m.Key]; !ok {
			f.Attach(m.Key, plant.New(plant.DefaultParams()))
		}
	}
	selfBatt := planSelfBatt(f, in)

	sets := map[string]float64{
		"ghi":    in.Weather.GHIWm2,
		"T_amb":  in.Weather.AmbientC,
		"P_load": math.Max(0, in.LoadW),
		"P_ev":   math.Max(0, in.EVW),
	}
	if sets["T_amb"] == 0 {
		sets["T_amb"] = 20
	}
	for i := 0; i < MaxUnits; i++ {
		idx := i + 1
		ctrl := plant.Control{}
		key := ""
		on := 0.0
		if i < len(f.slots) {
			key = f.slots[i]
			on = 1
			for _, m := range in.Members {
				if m.Key == key {
					ctrl = m.Control
					break
				}
			}
		}
		sets[fmt.Sprintf("u_on[%d]", idx)] = on
		sets[fmt.Sprintf("u_mode[%d]", idx)] = float64(ctrl.EMSMode)
		sets[fmt.Sprintf("u_cmd[%d]", idx)] = float64(ctrl.BatteryCmd)
		sets[fmt.Sprintf("u_sp[%d]", idx)] = ctrl.BatterySetpointW
		sets[fmt.Sprintf("u_self_batt[%d]", idx)] = selfBatt[i]
		limit := 0.0
		if ctrl.PVLimitEnable {
			limit = ctrl.PVLimitW
		}
		sets[fmt.Sprintf("u_pv_limit[%d]", idx)] = limit
	}
	if err := f.setReals(sets); err != nil {
		return plant.SiteResult{}, err
	}
	if !f.ready {
		if st := C.siteplant_enter_init(f.h, f.c); st != C.fmi2OK {
			return plant.SiteResult{}, fmt.Errorf("fmi2EnterInitializationMode %d", int(st))
		}
		if st := C.siteplant_exit_init(f.h, f.c); st != C.fmi2OK {
			return plant.SiteResult{}, fmt.Errorf("fmi2ExitInitializationMode %d", int(st))
		}
		f.ready = true
	}
	if dt > 0 {
		if st := C.siteplant_dostep(f.h, f.c, C.double(f.t), C.double(dt)); st != C.fmi2OK {
			return plant.SiteResult{}, fmt.Errorf("fmi2DoStep t=%v dt=%v status %d", f.t, dt, int(st))
		}
		f.t += dt
	}
	gets := []string{"P_meter"}
	for i := 1; i <= MaxUnits; i++ {
		gets = append(gets,
			fmt.Sprintf("P_pv[%d]", i),
			fmt.Sprintf("P_batt[%d]", i),
			fmt.Sprintf("soc[%d]", i),
		)
	}
	vals, err := f.getReals(gets)
	if err != nil {
		return plant.SiteResult{}, err
	}
	out := plant.SiteResult{
		GridW:   vals["P_meter"],
		LoadW:   math.Max(0, in.LoadW),
		EVW:     math.Max(0, in.EVW),
		Members: make([]plant.MemberResult, 0, len(f.slots)),
	}
	var check float64
	check += out.GridW + out.LoadW + out.EVW
	for i, key := range f.slots {
		idx := i + 1
		pPV := vals[fmt.Sprintf("P_pv[%d]", idx)]
		pBatt := vals[fmt.Sprintf("P_batt[%d]", idx)]
		soc := vals[fmt.Sprintf("soc[%d]", idx)]
		check += pPV + pBatt
		u := f.units[key]
		vBatt := 48.0
		ambient := in.Weather.AmbientC
		if ambient == 0 {
			ambient = 20
		}
		invC := ambient + 10
		if u != nil {
			if u.Params.BatteryNominalV > 0 {
				vBatt = u.Params.BatteryNominalV
			}
			u.State.SOC = soc
			invC = u.State.InverterC
		}
		aBatt := 0.0
		if pBatt != 0 && vBatt != 0 {
			aBatt = math.Abs(pBatt) / vBatt
		}
		mem := plant.Outputs{
			GridW:     out.GridW,
			PVW:       pPV,
			BatteryW:  pBatt,
			LoadW:     out.LoadW,
			EVW:       out.EVW,
			SOC:       soc,
			GridHz:    50,
			BatteryV:  vBatt,
			BatteryA:  aBatt,
			InverterC: invC,
			BatteryC:  ambient + 5,
			ResidualW: out.GridW + pPV + pBatt + out.LoadW + out.EVW,
		}
		if pPV < 0 {
			half := -pPV / 2
			v := 300.0
			if in.Weather.GHIWm2 > 0 {
				v = 300 + 80*(in.Weather.GHIWm2/1000)
			}
			a := 0.0
			if v > 0 {
				a = half / v
			}
			mem.MPPTs = [2]plant.MPPT{{V: v, A: a, W: half}, {V: v, A: a, W: half}}
			mem.PVDCW = -pPV
		}
		out.Members = append(out.Members, plant.MemberResult{Key: key, Out: mem})
	}
	out.ResidualW = check
	return out, nil
}

func planSelfBatt(f *FMU, in plant.SiteStep) [MaxUnits]float64 {
	var selfBatt [MaxUnits]float64
	pLoad := math.Max(0, in.LoadW)
	pEV := math.Max(0, in.EVW)
	var pPVsum, pBattEMS, slackChg, slackDis float64
	type row struct {
		i      int
		self   bool
		pPV    float64
		maxChg float64
		maxDis float64
		pBatt  float64
	}
	rows := make([]row, 0, MaxUnits)
	for i, key := range f.slots {
		u := f.units[key]
		if u == nil {
			u = plant.New(plant.DefaultParams())
		}
		ctrl := plant.Control{}
		for _, m := range in.Members {
			if m.Key == key {
				ctrl = m.Control
				break
			}
		}
		pPV, _ := u.PreviewPV(in.Weather, ctrl)
		self := ctrl.EMSMode != plant.ModeForced && ctrl.EMSMode != plant.ModeEMS
		pBatt := 0.0
		maxChg := u.Params.MaxChargeW
		maxDis := u.Params.MaxDischargeW
		if ctrl.MaxChargeW > 0 {
			maxChg = ctrl.MaxChargeW
		}
		if ctrl.MaxDischargeW > 0 {
			maxDis = ctrl.MaxDischargeW
		}
		if !self {
			switch ctrl.BatteryCmd {
			case plant.CmdCharge:
				pBatt = math.Abs(ctrl.BatterySetpointW)
			case plant.CmdDischarge:
				pBatt = -math.Abs(ctrl.BatterySetpointW)
			}
		}
		rows = append(rows, row{i: i, self: self, pPV: pPV, maxChg: maxChg, maxDis: maxDis, pBatt: pBatt})
		pPVsum += pPV
		if self {
			slackChg += maxChg
			slackDis += maxDis
		} else {
			pBattEMS += pBatt
		}
	}
	wantBatt := -(pLoad + pEV + pPVsum + pBattEMS)
	for _, r := range rows {
		if !r.self {
			selfBatt[r.i] = r.pBatt
			continue
		}
		share := 0.0
		if wantBatt > 0 && slackChg > 0 {
			share = wantBatt * (r.maxChg / slackChg)
		} else if wantBatt < 0 && slackDis > 0 {
			share = wantBatt * (r.maxDis / slackDis)
		}
		selfBatt[r.i] = share
	}
	return selfBatt
}

func (f *FMU) setReals(vals map[string]float64) error {
	vrs := make([]C.fmi2ValueReference, 0, len(vals))
	vs := make([]C.fmi2Real, 0, len(vals))
	for name, v := range vals {
		vr, ok := f.vr[name]
		if !ok {
			return fmt.Errorf("fmu missing variable %s", name)
		}
		vrs = append(vrs, C.fmi2ValueReference(vr))
		vs = append(vs, C.fmi2Real(v))
	}
	if len(vrs) == 0 {
		return nil
	}
	st := C.siteplant_set_real(f.h, f.c, &vrs[0], C.size_t(len(vrs)), &vs[0])
	if st != C.fmi2OK {
		return fmt.Errorf("fmi2SetReal status %d", int(st))
	}
	return nil
}

func (f *FMU) getReals(names []string) (map[string]float64, error) {
	vrs := make([]C.fmi2ValueReference, len(names))
	vs := make([]C.fmi2Real, len(names))
	for i, name := range names {
		vr, ok := f.vr[name]
		if !ok {
			return nil, fmt.Errorf("fmu missing variable %s", name)
		}
		vrs[i] = C.fmi2ValueReference(vr)
	}
	st := C.siteplant_get_real(f.h, f.c, &vrs[0], C.size_t(len(vrs)), &vs[0])
	if st != C.fmi2OK {
		return nil, fmt.Errorf("fmi2GetReal status %d", int(st))
	}
	out := make(map[string]float64, len(names))
	for i, name := range names {
		out[name] = float64(vs[i])
	}
	return out, nil
}

func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	root := filepath.Clean(dest) + string(os.PathSeparator)
	for _, f := range r.File {
		name := filepath.Clean(f.Name)
		if name == "." || strings.HasPrefix(name, "..") {
			continue
		}
		path := filepath.Join(dest, name)
		if path != filepath.Clean(dest) && !strings.HasPrefix(path, root) {
			continue
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		w, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(w, rc)
		w.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
