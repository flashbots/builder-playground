package flags

import (
	"testing"
	"time"

	flag "github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestParseFlags_BasicTypes(t *testing.T) {
	flagSet := flag.NewFlagSet("test", flag.ContinueOnError)

	type Config struct {
		Name          string        `flag:"name" description:"User name" default:"john"`
		Age           int           `flag:"age" description:"User age" default:"25"`
		Enabled       bool          `flag:"enabled" description:"Is enabled" default:"true"`
		Score         float64       `flag:"score" description:"Score" default:"99.5"`
		Timeout       time.Duration `flag:"timeout" description:"Request timeout" default:"30s"`
		OptionalValue *uint64       `flag:"optional" description:"Optional uint64 value"`
	}

	cfg := &Config{}
	require.NoError(t, ParseFlags(cfg, flagSet))

	require.Equal(t, "john", cfg.Name)
	require.Equal(t, 25, cfg.Age)
	require.Equal(t, true, cfg.Enabled)
	require.Equal(t, 99.5, cfg.Score)
	require.Equal(t, 30*time.Second, cfg.Timeout)
	require.Nil(t, cfg.OptionalValue)
}

func TestParseFlags_ActualParsing(t *testing.T) {
	flagSet := flag.NewFlagSet("test", flag.ContinueOnError)

	type Config struct {
		Host          string        `flag:"host" description:"Host address" default:"localhost"`
		Port          int           `flag:"port" description:"Port number" default:"8080"`
		Timeout       time.Duration `flag:"timeout" description:"Request timeout" default:"30s"`
		OptionalValue *uint64       `flag:"optional" description:"Optional uint64 value"`
	}

	cfg := &Config{}
	require.NoError(t, ParseFlags(cfg, flagSet))

	args := []string{"--host=example.com", "--port=9000", "--timeout=10s", "--optional=42"}
	err := flagSet.Parse(args)
	require.NoError(t, err)

	require.Equal(t, "example.com", cfg.Host)
	require.Equal(t, 9000, cfg.Port)
	require.Equal(t, 10*time.Second, cfg.Timeout)
	require.NotNil(t, cfg.OptionalValue)
	require.Equal(t, uint64(42), *cfg.OptionalValue)
}

func TestParseFlags_NestedStructs(t *testing.T) {
	flagSet := flag.NewFlagSet("test", flag.ContinueOnError)

	type Database struct {
		Host string `flag:"host" description:"Database host" default:"localhost"`
		Port int    `flag:"port" description:"Database port" default:"5432"`
	}

	type Config struct {
		AppName string   `flag:"app" description:"Application name" default:"myapp"`
		DB      Database `flag:"db"`
		DB2     Database
	}

	cfg := &Config{}
	require.NoError(t, ParseFlags(cfg, flagSet))

	args := []string{"--app=testapp", "--db.host=db.example.com", "--db.port=3306", "--host=flat.example.com", "--port=9999"}
	require.NoError(t, flagSet.Parse(args))

	require.Equal(t, "testapp", cfg.AppName)
	require.Equal(t, "db.example.com", cfg.DB.Host)
	require.Equal(t, 3306, cfg.DB.Port)
	require.Equal(t, "flat.example.com", cfg.DB2.Host)
	require.Equal(t, 9999, cfg.DB2.Port)
}
