# SiteEnergy (Modelica)

Lumped AC site plant: hybrids + house load + EV + grid slack.

```text
omc export.mos          # or: make fmu
→ Site4.fmu             # FMI 2.0 co-simulation, 4 inverters
```

The Go module `siteplant` loads that FMU when present (`SITEPLANT_FMU`, or
`modelica/Site4.fmu`). Otherwise the simulator uses the native DAE in
`internal/plant`, which implements the same bus equation.

`Site` keeps Modelica connectors. FMI matching cannot export that as a
causal CS FMU, so `Site4` is the flatten: named Real inputs/outputs, no
connectors. Mode-0 self-consumption slack is **not** inside the FMU; the
FMI master (`siteplant`) assigns `u_self_batt` each step.

Empty slots are gated with `u_on[i]=0` so unused inverters do not inject PV.

Requires [OpenModelica](https://openmodelica.org) (`omc` on PATH). The FMU
binary is linux/amd64; it is not checked in.
