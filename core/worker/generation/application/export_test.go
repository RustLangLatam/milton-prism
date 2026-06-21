package application

// Test exports — exposes unexported helpers for white-box unit tests.
// This file is compiled only during `go test`.

var (
	ExtractErrorVarNames      = extractErrorVarNames
	BuildMessageErrorGo       = buildMessageErrorGo
	ScanExistingErrorVarNames = scanExistingErrorVarNames
)
