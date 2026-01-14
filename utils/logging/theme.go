package logging

import (
	"log/slog"

	"github.com/phsym/console-slog"
)

type themeDef struct {
	name           string
	timestamp      console.ANSIMod
	source         console.ANSIMod
	message        console.ANSIMod
	messageDebug   console.ANSIMod
	attrKey        console.ANSIMod
	attrValue      console.ANSIMod
	attrValueError console.ANSIMod
	levelError     console.ANSIMod
	levelWarn      console.ANSIMod
	levelInfo      console.ANSIMod
	levelDebug     console.ANSIMod
}

func (t themeDef) Name() string                    { return t.name }
func (t themeDef) Timestamp() console.ANSIMod      { return t.timestamp }
func (t themeDef) Source() console.ANSIMod         { return t.source }
func (t themeDef) Message() console.ANSIMod        { return t.message }
func (t themeDef) MessageDebug() console.ANSIMod   { return t.messageDebug }
func (t themeDef) AttrKey() console.ANSIMod        { return t.attrKey }
func (t themeDef) AttrValue() console.ANSIMod      { return t.attrValue }
func (t themeDef) AttrValueError() console.ANSIMod { return t.attrValueError }
func (t themeDef) LevelError() console.ANSIMod     { return t.levelError }
func (t themeDef) LevelWarn() console.ANSIMod      { return t.levelWarn }
func (t themeDef) LevelInfo() console.ANSIMod      { return t.levelInfo }
func (t themeDef) LevelDebug() console.ANSIMod     { return t.levelDebug }
func (t themeDef) Level(level slog.Level) console.ANSIMod {
	switch {
	case level >= slog.LevelError:
		return t.LevelError()
	case level >= slog.LevelWarn:
		return t.LevelWarn()
	case level >= slog.LevelInfo:
		return t.LevelInfo()
	default:
		return t.LevelDebug()
	}
}

func newTheme() console.Theme {
	return themeDef{
		name:           "builder-playground",
		timestamp:      console.ToANSICode(console.BrightBlack),
		source:         console.ToANSICode(console.Bold, console.BrightBlack),
		message:        console.ToANSICode(console.Bold),
		messageDebug:   console.ToANSICode(console.Faint),
		attrKey:        console.ToANSICode(console.Yellow, console.Faint, console.Bold),
		attrValue:      console.ToANSICode(console.Faint),
		attrValueError: console.ToANSICode(console.Red),
		levelError:     console.ToANSICode(console.Red),
		levelWarn:      console.ToANSICode(console.Yellow),
		levelInfo:      console.ToANSICode(console.Faint),
		levelDebug:     console.ToANSICode(console.Cyan, console.Faint),
	}
}
