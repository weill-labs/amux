package server

import "strings"

const hostKeyVerificationSummary = "SSH host key verification failed"

func formatTakeoverFailureNotice(hostName, sshAddr string, err error) string {
	reason := summarizeTakeoverAttachError(err)
	target := strings.TrimSpace(hostName)
	if target == "" {
		target = "remote"
	}
	if sshAddr != "" && sshAddr != target {
		target += " (" + sshAddr + ")"
	}
	return "takeover " + target + ": " + reason
}

func summarizeTakeoverAttachError(err error) string {
	if err == nil {
		return "takeover failed"
	}

	text := strings.TrimSpace(err.Error())
	if text == "" {
		return "takeover failed"
	}
	if idx := strings.Index(text, hostKeyVerificationSummary); idx >= 0 {
		line := text[idx:]
		if nl := strings.IndexByte(line, '\n'); nl >= 0 {
			line = line[:nl]
		}
		return strings.TrimSpace(line)
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "amux:"))
		if line != "" {
			return line
		}
	}
	return "takeover failed"
}
