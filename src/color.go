package main

import "os"

type colorer struct{ enabled bool }

func newColorer(w any) colorer {
	if os.Getenv("NO_COLOR") != "" {
		return colorer{}
	}
	f, ok := w.(*os.File)
	if !ok {
		return colorer{}
	}
	fi, err := f.Stat()
	if err != nil {
		return colorer{}
	}
	return colorer{enabled: fi.Mode()&os.ModeCharDevice != 0}
}

func (c colorer) error(s string) string   { return c.wrap("31", s) }
func (c colorer) success(s string) string { return c.wrap("32", s) }
func (c colorer) warning(s string) string { return c.wrap("33", s) }

func (c colorer) wrap(code, s string) string {
	if !c.enabled {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}
