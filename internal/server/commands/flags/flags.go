package flags

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type FlagType int

const (
	FlagTypeString FlagType = iota
	FlagTypeDuration
	FlagTypeBool
	FlagTypeInt
)

type FlagSpec struct {
	Name    string
	Type    FlagType
	Default any
}

type ParsedFlags struct {
	values      map[string]any
	seen        map[string]bool
	positionals []string
}

func ParseCommandFlags(args []string, specs []FlagSpec) (ParsedFlags, error) {
	return parseCommandFlags(args, specs)
}

func parseCommandFlags(args []string, specs []FlagSpec) (ParsedFlags, error) {
	parsed := ParsedFlags{
		values: make(map[string]any, len(specs)),
		seen:   make(map[string]bool, len(specs)),
	}
	byName := make(map[string]FlagSpec, len(specs))
	for _, spec := range specs {
		byName[spec.Name] = spec
		parsed.values[spec.Name] = spec.Default
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		spec, ok := byName[arg]
		if !ok {
			if strings.HasPrefix(arg, "-") {
				return ParsedFlags{}, fmt.Errorf("unknown flag: %s", arg)
			}
			parsed.positionals = append(parsed.positionals, arg)
			continue
		}
		parsed.seen[arg] = true

		switch spec.Type {
		case FlagTypeString:
			if i+1 >= len(args) {
				return ParsedFlags{}, fmt.Errorf("missing value for %s", spec.Name)
			}
			i++
			parsed.values[spec.Name] = args[i]
		case FlagTypeDuration:
			if i+1 >= len(args) {
				return ParsedFlags{}, fmt.Errorf("missing value for %s", spec.Name)
			}
			i++
			value, err := time.ParseDuration(args[i])
			if err != nil {
				return ParsedFlags{}, fmt.Errorf("invalid value for %s: %s", spec.Name, args[i])
			}
			parsed.values[spec.Name] = value
		case FlagTypeBool:
			parsed.values[spec.Name] = true
		case FlagTypeInt:
			if i+1 >= len(args) {
				return ParsedFlags{}, fmt.Errorf("missing value for %s", spec.Name)
			}
			i++
			value, err := strconv.Atoi(args[i])
			if err != nil {
				return ParsedFlags{}, fmt.Errorf("invalid value for %s: %s", spec.Name, args[i])
			}
			parsed.values[spec.Name] = value
		default:
			return ParsedFlags{}, fmt.Errorf("unknown flag type for %s", spec.Name)
		}
	}

	return parsed, nil
}

func (p ParsedFlags) String(name string) string {
	value, _ := p.values[name].(string)
	return value
}

func (p ParsedFlags) Duration(name string) time.Duration {
	value, _ := p.values[name].(time.Duration)
	return value
}

func (p ParsedFlags) Bool(name string) bool {
	value, _ := p.values[name].(bool)
	return value
}

func (p ParsedFlags) Int(name string) int {
	value, _ := p.values[name].(int)
	return value
}

func (p ParsedFlags) Seen(name string) bool {
	return p.seen[name]
}

func (p ParsedFlags) Positionals() []string {
	return append([]string(nil), p.positionals...)
}
