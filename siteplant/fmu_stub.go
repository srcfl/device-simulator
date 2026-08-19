//go:build !cgo || !linux || !amd64

package siteplant

import (
	"fmt"

	"github.com/srcfl/device-simulator/internal/plant"
)

func LoadFMU(path string) (plant.Model, error) {
	return nil, fmt.Errorf("siteplant: FMU loader needs CGO on linux/amd64 (file %s)", path)
}
