package ocpp

import (
	"encoding/json"
	"time"
)

// OCPP 1.6 Message Types
const (
	MessageTypeCall       = 2
	MessageTypeCallResult = 3
	MessageTypeCallError  = 4
)

// OCPP 1.6 Actions
const (
	ActionBootNotification       = "BootNotification"
	ActionHeartbeat              = "Heartbeat"
	ActionStatusNotification     = "StatusNotification"
	ActionStartTransaction       = "StartTransaction"
	ActionStopTransaction        = "StopTransaction"
	ActionMeterValues            = "MeterValues"
	ActionRemoteStartTransaction = "RemoteStartTransaction"
	ActionRemoteStopTransaction  = "RemoteStopTransaction"
	ActionChangeConfiguration    = "ChangeConfiguration"
	ActionGetConfiguration       = "GetConfiguration"
	ActionReset                  = "Reset"
	ActionSetChargingProfile     = "SetChargingProfile"
	ActionClearChargingProfile   = "ClearChargingProfile"
	ActionGetLocalListVersion    = "GetLocalListVersion"
	ActionSendLocalList          = "SendLocalList"
)

// Connector Status values (OCPP 1.6)
const (
	StatusAvailable     = "Available"     // No vehicle plugged in, ready for new vehicle
	StatusPreparing     = "Preparing"     // Vehicle plugged in, waiting for auth/communication
	StatusCharging      = "Charging"      // Actively charging
	StatusSuspendedEVSE = "SuspendedEVSE" // Vehicle plugged in, charger not delivering power (charger-side reason)
	StatusSuspendedEV   = "SuspendedEV"   // Vehicle plugged in, vehicle not accepting power (vehicle-side reason)
	StatusFinishing     = "Finishing"     // Charging ended, vehicle still plugged in
	StatusReserved      = "Reserved"      // Reserved for specific user/vehicle
	StatusUnavailable   = "Unavailable"   // Not available (maintenance, restart pending)
	StatusFaulted       = "Faulted"       // Experiencing fault or issue
)

// BootNotificationRequest represents a BootNotification.req
type BootNotificationRequest struct {
	ChargePointVendor       string `json:"chargePointVendor"`
	ChargePointModel        string `json:"chargePointModel"`
	ChargePointSerialNumber string `json:"chargePointSerialNumber,omitempty"`
	ChargeBoxSerialNumber   string `json:"chargeBoxSerialNumber,omitempty"`
	FirmwareVersion         string `json:"firmwareVersion,omitempty"`
	Iccid                   string `json:"iccid,omitempty"`
	Imsi                    string `json:"imsi,omitempty"`
	MeterType               string `json:"meterType,omitempty"`
	MeterSerialNumber       string `json:"meterSerialNumber,omitempty"`
}

// BootNotificationResponse represents a BootNotification.conf
type BootNotificationResponse struct {
	Status      string `json:"status"`
	CurrentTime string `json:"currentTime"`
	Interval    int    `json:"interval"`
}

// HeartbeatRequest represents a Heartbeat.req
type HeartbeatRequest struct{}

// HeartbeatResponse represents a Heartbeat.conf
type HeartbeatResponse struct {
	CurrentTime string `json:"currentTime"`
}

// StatusNotificationRequest represents a StatusNotification.req
type StatusNotificationRequest struct {
	ConnectorId     int    `json:"connectorId"`
	ErrorCode       string `json:"errorCode"`
	Status          string `json:"status"`
	Timestamp       string `json:"timestamp,omitempty"`
	Info            string `json:"info,omitempty"`
	VendorId        string `json:"vendorId,omitempty"`
	VendorErrorCode string `json:"vendorErrorCode,omitempty"`
}

// StatusNotificationResponse represents a StatusNotification.conf
type StatusNotificationResponse struct{}

// StartTransactionRequest represents a StartTransaction.req
type StartTransactionRequest struct {
	ConnectorId   int    `json:"connectorId"`
	IdTag         string `json:"idTag"`
	MeterStart    int    `json:"meterStart"`
	Timestamp     string `json:"timestamp"`
	ReservationId int    `json:"reservationId,omitempty"`
}

// StartTransactionResponse represents a StartTransaction.conf
type StartTransactionResponse struct {
	IdTagInfo     IdTagInfo `json:"idTagInfo"`
	TransactionId int       `json:"transactionId"`
}

// StopTransactionRequest represents a StopTransaction.req
type StopTransactionRequest struct {
	IdTag           string       `json:"idTag,omitempty"`
	MeterStop       int          `json:"meterStop"`
	Timestamp       string       `json:"timestamp"`
	TransactionId   int          `json:"transactionId"`
	Reason          string       `json:"reason,omitempty"`
	TransactionData []MeterValue `json:"transactionData,omitempty"`
}

// StopTransactionResponse represents a StopTransaction.conf
type StopTransactionResponse struct {
	IdTagInfo *IdTagInfo `json:"idTagInfo,omitempty"`
}

// MeterValuesRequest represents a MeterValues.req
type MeterValuesRequest struct {
	ConnectorId   int          `json:"connectorId"`
	TransactionId int          `json:"transactionId,omitempty"`
	MeterValue    []MeterValue `json:"meterValue"`
}

// MeterValuesResponse represents a MeterValues.conf
type MeterValuesResponse struct{}

// MeterValue represents a meter value sample
type MeterValue struct {
	Timestamp    string         `json:"timestamp"`
	SampledValue []SampledValue `json:"sampledValue"`
}

// SampledValue represents a single sampled value
type SampledValue struct {
	Value     string `json:"value"`
	Context   string `json:"context,omitempty"`
	Format    string `json:"format,omitempty"`
	Measurand string `json:"measurand,omitempty"`
	Phase     string `json:"phase,omitempty"`
	Location  string `json:"location,omitempty"`
	Unit      string `json:"unit,omitempty"`
}

// IdTagInfo represents authorization info
type IdTagInfo struct {
	ExpiryDate  string `json:"expiryDate,omitempty"`
	ParentIdTag string `json:"parentIdTag,omitempty"`
	Status      string `json:"status"`
}

// RemoteStartTransactionRequest represents a RemoteStartTransaction.req from central system
type RemoteStartTransactionRequest struct {
	ConnectorId     int              `json:"connectorId,omitempty"`
	IdTag           string           `json:"idTag"`
	ChargingProfile *ChargingProfile `json:"chargingProfile,omitempty"`
}

// RemoteStartTransactionResponse represents a RemoteStartTransaction.conf
type RemoteStartTransactionResponse struct {
	Status string `json:"status"`
}

// RemoteStopTransactionRequest represents a RemoteStopTransaction.req from central system
type RemoteStopTransactionRequest struct {
	TransactionId int `json:"transactionId"`
}

// RemoteStopTransactionResponse represents a RemoteStopTransaction.conf
type RemoteStopTransactionResponse struct {
	Status string `json:"status"`
}

// GetConfigurationRequest represents a GetConfiguration.req from central system
type GetConfigurationRequest struct {
	Key []string `json:"key,omitempty"`
}

// ConfigurationKey represents a single configuration key-value pair
type ConfigurationKey struct {
	Key      string `json:"key"`
	Readonly bool   `json:"readonly"`
	Value    string `json:"value,omitempty"`
}

// GetConfigurationResponse represents a GetConfiguration.conf
type GetConfigurationResponse struct {
	ConfigurationKey []ConfigurationKey `json:"configurationKey,omitempty"`
	UnknownKey       []string           `json:"unknownKey,omitempty"`
}

// ChangeConfigurationRequest represents a ChangeConfiguration.req from central system
type ChangeConfigurationRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ChangeConfigurationResponse represents a ChangeConfiguration.conf
type ChangeConfigurationResponse struct {
	Status string `json:"status"` // Accepted, Rejected, RebootRequired, NotSupported
}

// ResetRequest represents a Reset.req from central system
type ResetRequest struct {
	Type string `json:"type"` // Hard or Soft
}

// ResetResponse represents a Reset.conf
type ResetResponse struct {
	Status string `json:"status"` // Accepted, Rejected
}

// ChargingProfile represents a ChargingProfile object (OCPP 1.6)
// Note: Date fields are kept as strings to avoid parsing at the emulator layer.
type ChargingProfile struct {
	ChargingProfileId      int64            `json:"chargingProfileId"`
	TransactionId          *int64           `json:"transactionId,omitempty"`
	StackLevel             int              `json:"stackLevel"`
	ChargingProfilePurpose string           `json:"chargingProfilePurpose"`
	ChargingProfileKind    string           `json:"chargingProfileKind"`
	RecurrencyKind         string           `json:"recurrencyKind,omitempty"`
	ValidFrom              string           `json:"validFrom,omitempty"`
	ValidTo                string           `json:"validTo,omitempty"`
	ChargingSchedule       ChargingSchedule `json:"chargingSchedule"`
}

// ChargingSchedule represents a ChargingSchedule object (OCPP 1.6)
type ChargingSchedule struct {
	Duration               *int64                   `json:"duration,omitempty"`
	StartSchedule          string                   `json:"startSchedule,omitempty"`
	ChargingRateUnit       string                   `json:"chargingRateUnit"`
	ChargingSchedulePeriod []ChargingSchedulePeriod `json:"chargingSchedulePeriod"`
	MinChargingRate        *float64                 `json:"minChargingRate,omitempty"`
}

// ChargingSchedulePeriod represents a ChargingSchedulePeriod object (OCPP 1.6)
type ChargingSchedulePeriod struct {
	StartPeriod  int64   `json:"startPeriod"`
	Limit        float64 `json:"limit"`
	NumberPhases *int    `json:"numberPhases,omitempty"`
}

// SetChargingProfileRequest represents a SetChargingProfile.req
type SetChargingProfileRequest struct {
	ConnectorId        int             `json:"connectorId"`
	CsChargingProfiles ChargingProfile `json:"csChargingProfiles"`
}

// SetChargingProfileResponse represents a SetChargingProfile.conf
type SetChargingProfileResponse struct {
	Status string `json:"status"`
}

// ClearChargingProfileRequest represents a ClearChargingProfile.req
type ClearChargingProfileRequest struct {
	Id                     *int64 `json:"id,omitempty"`
	ConnectorId            *int   `json:"connectorId,omitempty"`
	ChargingProfilePurpose string `json:"chargingProfilePurpose,omitempty"`
	StackLevel             *int   `json:"stackLevel,omitempty"`
}

// ClearChargingProfileResponse represents a ClearChargingProfile.conf
type ClearChargingProfileResponse struct {
	Status string `json:"status"`
}

// GetLocalListVersionRequest represents a GetLocalListVersion.req from central system
type GetLocalListVersionRequest struct{}

// GetLocalListVersionResponse represents a GetLocalListVersion.conf
type GetLocalListVersionResponse struct {
	ListVersion int `json:"listVersion"`
}

// SendLocalListRequest represents a SendLocalList.req from central system
type SendLocalListRequest struct {
	ListVersion            int                 `json:"listVersion"`
	LocalAuthorizationList []AuthorizationData `json:"localAuthorizationList,omitempty"`
	UpdateType             string              `json:"updateType"`
}

// AuthorizationData represents an authorization entry in the local list
type AuthorizationData struct {
	IdTag     string    `json:"idTag"`
	IdTagInfo IdTagInfo `json:"idTagInfo,omitempty"`
}

// SendLocalListResponse represents a SendLocalList.conf
type SendLocalListResponse struct {
	Status string `json:"status"`
}

// ParseOCPPMessage parses an OCPP WebSocket message
func ParseOCPPMessage(data []byte) (messageType int, messageId string, action string, payload json.RawMessage, err error) {
	var raw []json.RawMessage
	if err = json.Unmarshal(data, &raw); err != nil {
		return
	}

	if len(raw) < 3 {
		err = ErrInvalidMessage
		return
	}

	if err = json.Unmarshal(raw[0], &messageType); err != nil {
		return
	}

	if err = json.Unmarshal(raw[1], &messageId); err != nil {
		return
	}

	if messageType == MessageTypeCall {
		if len(raw) < 4 {
			err = ErrInvalidMessage
			return
		}
		if err = json.Unmarshal(raw[2], &action); err != nil {
			return
		}
		payload = raw[3]
	} else {
		payload = raw[2]
	}

	return
}

// CreateCallMessage creates an OCPP Call message
func CreateCallMessage(messageId, action string, payload interface{}) ([]byte, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	msg := []interface{}{MessageTypeCall, messageId, action, json.RawMessage(payloadBytes)}
	return json.Marshal(msg)
}

// CreateCallResultMessage creates an OCPP CallResult message
func CreateCallResultMessage(messageId string, payload interface{}) ([]byte, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	msg := []interface{}{MessageTypeCallResult, messageId, json.RawMessage(payloadBytes)}
	return json.Marshal(msg)
}

// CreateCallErrorMessage creates an OCPP CallError message
func CreateCallErrorMessage(messageId, errorCode, errorDescription string, errorDetails interface{}) ([]byte, error) {
	detailsBytes, err := json.Marshal(errorDetails)
	if err != nil {
		return nil, err
	}

	msg := []interface{}{MessageTypeCallError, messageId, errorCode, errorDescription, json.RawMessage(detailsBytes)}
	return json.Marshal(msg)
}

// FormatTimestamp formats a time as OCPP timestamp
func FormatTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// Error types
var (
	ErrInvalidMessage = &OCPPError{Code: "FormationViolation", Description: "Invalid message format"}
)

// OCPPError represents an OCPP error
type OCPPError struct {
	Code        string
	Description string
}

func (e *OCPPError) Error() string {
	return e.Code + ": " + e.Description
}
