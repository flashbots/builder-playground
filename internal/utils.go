package internal

import (
	"fmt"
	"strconv"
)

type nullableUint64Value struct {
	ptr **uint64
}

func (n nullableUint64Value) String() string {
	if *n.ptr == nil {
		return "nil"
	}
	return fmt.Sprintf("%d", **n.ptr)
}

func (n nullableUint64Value) Set(s string) error {
	if s == "" || s == "nil" {
		*n.ptr = nil
		return nil
	}

	val, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return err
	}
	*n.ptr = &val
	return nil
}

func (n nullableUint64Value) Type() string {
	return "uint64"
}

func (n nullableUint64Value) GetNoOptDefVal() string {
	return "0"
}
