package flags

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	flag "github.com/spf13/pflag"
)

// ParseFlags reads struct tags and automatically generates flags.
// Supported tag format: `flag:"name" description:"desc" default:"value"`
// The struct fields must be exported and of supported types (string, int, int64, bool, uint, uint64, float64).
func ParseFlags(cfg interface{}, flagSet *flag.FlagSet) error {
	v := reflect.ValueOf(cfg)
	if v.Kind() != reflect.Ptr {
		return fmt.Errorf("config must be a pointer to struct")
	}

	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("config must be a pointer to struct")
	}

	t := v.Type()

	return parseStruct(v, t, "", flagSet)
}

func parseStruct(v reflect.Value, t reflect.Type, prefix string, flagSet *flag.FlagSet) error {
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		if !field.CanSet() {
			continue // Skip unexported fields
		}

		// Handle nested structs
		if field.Kind() == reflect.Struct {
			nestedPrefix := prefix
			if flagPrefix := fieldType.Tag.Get("flag"); flagPrefix != "" {
				nestedPrefix = prefix + flagPrefix + "."
			}
			if err := parseStruct(field, field.Type(), nestedPrefix, flagSet); err != nil {
				return err
			}
			continue
		}

		flagName := fieldType.Tag.Get("flag")
		if flagName == "" {
			continue // Skip fields without flag tag
		}

		description := fieldType.Tag.Get("description")
		defaultValue := fieldType.Tag.Get("default")

		fullFlagName := prefix + flagName
		if err := registerFlag(field, fullFlagName, description, defaultValue, flagSet); err != nil {
			return fmt.Errorf("failed to register flag %s: %w", fullFlagName, err)
		}
	}

	return nil
}

func registerFlag(field reflect.Value, name, description, defaultValue string, flagSet *flag.FlagSet) error {
	switch field.Kind() {
	case reflect.String:
		def := defaultValue
		ptr := field.Addr().Interface().(*string)
		flagSet.StringVar(ptr, name, def, description)

	case reflect.Int:
		def := 0
		if defaultValue != "" {
			var err error
			def, err = strconv.Atoi(defaultValue)
			if err != nil {
				return fmt.Errorf("invalid default value for int: %s", defaultValue)
			}
		}
		ptr := field.Addr().Interface().(*int)
		flagSet.IntVar(ptr, name, def, description)

	case reflect.Int64:
		def := int64(0)
		if defaultValue != "" {
			var err error
			def, err = strconv.ParseInt(defaultValue, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid default value for int64: %s", defaultValue)
			}
		}
		ptr := field.Addr().Interface().(*int64)
		flagSet.Int64Var(ptr, name, def, description)

	case reflect.Bool:
		def := false
		if defaultValue != "" {
			var err error
			def, err = strconv.ParseBool(defaultValue)
			if err != nil {
				return fmt.Errorf("invalid default value for bool: %s", defaultValue)
			}
		}
		ptr := field.Addr().Interface().(*bool)
		flagSet.BoolVar(ptr, name, def, description)

	case reflect.Uint:
		def := uint(0)
		if defaultValue != "" {
			val, err := strconv.ParseUint(defaultValue, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid default value for uint: %s", defaultValue)
			}
			def = uint(val)
		}
		ptr := field.Addr().Interface().(*uint)
		flagSet.UintVar(ptr, name, def, description)

	case reflect.Uint64:
		def := uint64(0)
		if defaultValue != "" {
			var err error
			def, err = strconv.ParseUint(defaultValue, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid default value for uint64: %s", defaultValue)
			}
		}
		ptr := field.Addr().Interface().(*uint64)
		flagSet.Uint64Var(ptr, name, def, description)

	case reflect.Float64:
		def := float64(0)
		if defaultValue != "" {
			var err error
			def, err = strconv.ParseFloat(defaultValue, 64)
			if err != nil {
				return fmt.Errorf("invalid default value for float64: %s", defaultValue)
			}
		}
		ptr := field.Addr().Interface().(*float64)
		flagSet.Float64Var(ptr, name, def, description)

	case reflect.Slice:
		// Handle string slices
		if field.Type().Elem().Kind() == reflect.String {
			def := []string{}
			if defaultValue != "" {
				def = strings.Split(defaultValue, ",")
			}

			// Create a custom flag.Value for string slices
			sliceFlag := &stringSliceFlag{
				target: field.Addr().Interface().(*[]string),
				value:  def,
			}
			*sliceFlag.target = def
			flagSet.Var(sliceFlag, name, description)
		} else {
			return fmt.Errorf("unsupported slice type: %v", field.Type())
		}

	default:
		return fmt.Errorf("unsupported field type: %v", field.Kind())
	}

	return nil
}

// stringSliceFlag implements pflag.Value for []string
type stringSliceFlag struct {
	target *[]string
	value  []string
}

func (s *stringSliceFlag) String() string {
	return strings.Join(s.value, ",")
}

func (s *stringSliceFlag) Set(val string) error {
	s.value = strings.Split(val, ",")
	*s.target = s.value
	return nil
}

func (s *stringSliceFlag) Type() string {
	return "stringSlice"
}
