// Package remoteref parses and formats canonical amux remote object refs.
package remoteref

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type Kind string

const (
	KindPane   Kind = "pane"
	KindWindow Kind = "window"
)

type SelectorKind string

const (
	SelectorName  SelectorKind = "name"
	SelectorID    SelectorKind = "id"
	SelectorIndex SelectorKind = "index"
)

type Ref struct {
	Remote       string
	Session      string
	Kind         Kind
	SelectorKind SelectorKind
	Selector     string
}

func Parse(value string) (Ref, error) {
	u, err := url.Parse(value)
	if err != nil {
		return Ref{}, fmt.Errorf("parse remote ref: %w", err)
	}
	if u.Scheme != "amux" {
		return Ref{}, fmt.Errorf("remote ref scheme must be amux")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return Ref{}, fmt.Errorf("remote ref must not include user info, query, or fragment")
	}
	remote, err := url.PathUnescape(u.Host)
	if err != nil {
		return Ref{}, fmt.Errorf("decode remote: %w", err)
	}
	if remote == "" {
		return Ref{}, fmt.Errorf("remote ref requires remote")
	}

	parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	if len(parts) != 4 {
		return Ref{}, fmt.Errorf("remote ref requires session, kind, selector kind, and selector")
	}
	session, err := decodeSegment("session", parts[0])
	if err != nil {
		return Ref{}, err
	}
	kindText, err := decodeSegment("kind", parts[1])
	if err != nil {
		return Ref{}, err
	}
	selectorKindText, err := decodeSegment("selector kind", parts[2])
	if err != nil {
		return Ref{}, err
	}
	selector, err := decodeSegment("selector", parts[3])
	if err != nil {
		return Ref{}, err
	}

	ref := Ref{
		Remote:       remote,
		Session:      session,
		Kind:         Kind(kindText),
		SelectorKind: SelectorKind(selectorKindText),
		Selector:     selector,
	}
	if err := validate(ref); err != nil {
		return Ref{}, err
	}
	return ref, nil
}

func Format(ref Ref) (string, error) {
	if err := validate(ref); err != nil {
		return "", err
	}
	return "amux://" + url.PathEscape(ref.Remote) + "/" +
		url.PathEscape(ref.Session) + "/" +
		string(ref.Kind) + "/" +
		string(ref.SelectorKind) + "/" +
		url.PathEscape(ref.Selector), nil
}

func decodeSegment(name, value string) (string, error) {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return "", fmt.Errorf("decode %s: %w", name, err)
	}
	if decoded == "" {
		return "", fmt.Errorf("remote ref requires %s", name)
	}
	return decoded, nil
}

func validate(ref Ref) error {
	if ref.Remote == "" {
		return fmt.Errorf("remote ref requires remote")
	}
	if ref.Session == "" {
		return fmt.Errorf("remote ref requires session")
	}
	if ref.Selector == "" {
		return fmt.Errorf("remote ref requires selector")
	}
	switch ref.Kind {
	case KindPane:
		switch ref.SelectorKind {
		case SelectorName:
			return nil
		case SelectorID:
			return validatePositiveInt("pane id", ref.Selector)
		case SelectorIndex:
			return fmt.Errorf("pane refs do not support index selectors")
		default:
			return fmt.Errorf("unknown selector kind %q", ref.SelectorKind)
		}
	case KindWindow:
		switch ref.SelectorKind {
		case SelectorName:
			return nil
		case SelectorIndex:
			return validatePositiveInt("window index", ref.Selector)
		case SelectorID:
			return fmt.Errorf("window refs do not support id selectors")
		default:
			return fmt.Errorf("unknown selector kind %q", ref.SelectorKind)
		}
	default:
		return fmt.Errorf("unknown remote object kind %q", ref.Kind)
	}
}

func validatePositiveInt(name, value string) error {
	n, err := strconv.ParseUint(value, 10, 32)
	if err != nil || n == 0 {
		return fmt.Errorf("%s must be a positive integer", name)
	}
	return nil
}
