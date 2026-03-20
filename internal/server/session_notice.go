package server

import (
	"os"
	"time"
)

const defaultSessionNoticeDuration = 5 * time.Second

type sessionNoticeSetResult struct {
	token uint64
}

type sessionNoticeSetCmd struct {
	message string
	reply   chan sessionNoticeSetResult
}

func (e sessionNoticeSetCmd) handle(s *Session) {
	s.notice = e.message
	s.noticeToken++
	token := s.noticeToken
	e.reply <- sessionNoticeSetResult{token: token}
	s.broadcastLayoutNow()
}

type sessionNoticeClearCmd struct {
	token uint64
}

func (e sessionNoticeClearCmd) handle(s *Session) {
	if e.token != s.noticeToken || s.notice == "" {
		return
	}
	s.notice = ""
	s.broadcastLayoutNow()
}

func (s *Session) showSessionNotice(message string) {
	if message == "" {
		return
	}

	res := s.enqueueSessionNoticeSet(message)
	if res.token == 0 {
		return
	}

	timer := time.NewTimer(sessionNoticeDuration())
	go func(token uint64) {
		defer timer.Stop()
		select {
		case <-timer.C:
			s.enqueueSessionNoticeClear(token)
		case <-s.sessionEventDone:
		}
	}(res.token)
}

func (s *Session) enqueueSessionNoticeSet(message string) sessionNoticeSetResult {
	reply := make(chan sessionNoticeSetResult, 1)
	if !s.enqueueEvent(sessionNoticeSetCmd{message: message, reply: reply}) {
		return sessionNoticeSetResult{}
	}

	select {
	case res := <-reply:
		return res
	case <-s.sessionEventDone:
		select {
		case res := <-reply:
			return res
		default:
			return sessionNoticeSetResult{}
		}
	}
}

func (s *Session) enqueueSessionNoticeClear(token uint64) {
	s.enqueueEvent(sessionNoticeClearCmd{token: token})
}

func sessionNoticeDuration() time.Duration {
	if value := os.Getenv("AMUX_NOTICE_DURATION"); value != "" {
		if d, err := time.ParseDuration(value); err == nil && d > 0 {
			return d
		}
	}
	return defaultSessionNoticeDuration
}
