package matter

// This package drives real, commissionable matter.js ServerNodes via a
// supervised Node.js child process. The Go side is the source of truth for
// device state and physics; the Node bridge is a thin Matter shim. See
// internal/matter/bridge for the Node program and doc.go for the protocol.

// DeviceConfig is the payload sent to the bridge to create a Matter ServerNode.
// The Go side assigns NodeID, Passcode and Discriminator so that pairing codes
// remain stable across bridge restarts / replays.
type DeviceConfig struct {
	NodeID        string             `json:"nodeId"`
	Type          string             `json:"type"` // bridge device-type key, see devicetypes.go
	Serial        string             `json:"serial"`
	Port          int                `json:"port"`
	StorageDir    string             `json:"storageDir"`
	VendorID      int                `json:"vendorId"`
	ProductID     int                `json:"productId"`
	Passcode      int                `json:"passcode"`
	Discriminator int                `json:"discriminator"`
	DeviceName    string             `json:"deviceName"`
	Defaults      map[string]float64 `json:"defaults,omitempty"`
}

// CommissioningInfo holds the pairing details returned by the bridge after a
// ServerNode is created, plus its live commissioning status.
type CommissioningInfo struct {
	NodeID            string `json:"nodeId"`
	ManualPairingCode string `json:"manualPairingCode"`
	QRPairingCode     string `json:"qrPairingCode"` // "MT:..." payload, render as QR client-side
	Port              int    `json:"port"`
	Commissioned      bool   `json:"commissioned"`
	FabricCount       int    `json:"fabricCount"`
}

// AttrChange is a single Matter attribute write pushed from Go to the bridge.
type AttrChange struct {
	Cluster   string      `json:"cluster"`
	Attribute string      `json:"attribute"`
	Value     interface{} `json:"value"`
}

// ErrNodeUnavailable is returned when Node.js or the matter.js bridge cannot be
// used, so callers (createSimulator, the HTTP/MCP layer) can degrade gracefully
// instead of crashing.
type ErrNodeUnavailable struct {
	Reason string
}

func (e *ErrNodeUnavailable) Error() string {
	return "matter unavailable: " + e.Reason
}

// Default Matter identity values. Vendor 0xFFF1 (65521) and product 0x8000 are
// the CSA test vendor/product IDs, appropriate for a simulator.
const (
	DefaultVendorID  = 0xFFF1
	DefaultProductID = 0x8000
)
