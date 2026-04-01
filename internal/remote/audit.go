package remote

import (
	charmlog "github.com/charmbracelet/log"
	"github.com/weill-labs/amux/internal/auditlog"
)

func (m *Manager) SetLogger(logger *charmlog.Logger) {
	if m == nil {
		return
	}
	if logger == nil {
		logger = auditlog.Discard()
	}
	m.logger = logger
	for hostName, hc := range m.hosts {
		if hc != nil {
			hc.logger = logger.With("host", hostName)
		}
	}
}

func (hc *HostConn) logSSHConnect() {
	if hc == nil {
		return
	}
	hc.logger.Info("ssh connected",
		"event", "ssh_connect",
		"host", hc.name,
		"ssh_addr", hc.connectAddr,
		"session", hc.sessionName,
		"takeover", hc.takeoverMode,
		"pane_count", len(hc.localToRemote),
	)
}

func (hc *HostConn) logSSHDisconnect(level charmlog.Level, reason string) {
	if hc == nil {
		return
	}
	auditlog.LogWithLevel(hc.logger, level, "ssh disconnected",
		"event", "ssh_disconnect",
		"host", hc.name,
		"ssh_addr", hc.connectAddr,
		"session", hc.sessionName,
		"takeover", hc.takeoverMode,
		"reason", reason,
	)
}

func (m *Manager) logDeployFailure(hostName, stage string, err error) {
	if m == nil || err == nil {
		return
	}
	m.logger.Warn("ssh deploy failed",
		"event", "ssh_deploy",
		"host", hostName,
		"stage", stage,
		"error", err,
	)
}
