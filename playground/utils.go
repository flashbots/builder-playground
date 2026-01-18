package playground

import (
	"fmt"
	"strings"
)

type MapStringFlag map[string]string

func (n *MapStringFlag) String() string {
	parts := []string{}
	for k, v := range *n {
		parts = append(parts, k+"="+v)
	}
	return "(" + strings.Join(parts, ",") + ")"
}

func (n *MapStringFlag) Type() string {
	return "map(string, string)"
}

func (n *MapStringFlag) Set(s string) error {
	parts := strings.Split(s, "=")
	if len(parts) != 2 {
		return fmt.Errorf("expected k=v for flag")
	}

	k := parts[0]
	v := parts[1]

	if *n == nil {
		(*n) = map[string]string{}
	}
	(*n)[k] = v
	return nil
}
