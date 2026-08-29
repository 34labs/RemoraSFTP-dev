package server

import (
	"time"

	"remorasftp/internal/manager"
	"remorasftp/internal/protocol"
)

// managerInjectClient installs a connected session backed by the provided
// client into the manager. Test-only.
func managerInjectClient(mgr *manager.Manager, sessionID string, cl protocol.Client) {
	caps := cl.Capabilities()
	mgr.InjectTestSession(sessionID, manager.TestSession{
		ConnID:      "test-conn",
		ConnName:    "Test",
		Protocol:    caps.Protocol,
		Host:        "test.local",
		StartDir:    "/",
		CWD:         "/",
		Client:      cl,
		ConnectedAt: time.Now(),
	})
}
