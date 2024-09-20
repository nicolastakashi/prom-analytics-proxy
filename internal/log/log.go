// Copyright 2024 The prometheus-operator Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package log

import (
	"errors"
	"flag"
	"log/slog"
	"os"
)

var (
	logLevel  string
	logFormat string
)

func RegisterFlags(flagSet *flag.FlagSet) {
	flagSet.StringVar(&logLevel, "log-level", slog.LevelInfo.String(), "Log level")
	flagSet.StringVar(&logFormat, "log-format", "text", "Log format (text, json)")
}

func parseLogLevel() (*slog.Level, error) {
	level := slog.Level(1)
	err := level.UnmarshalText([]byte(logLevel))
	if err != nil {
		return nil, err
	}
	return &level, nil
}

func NewLogger() (*slog.Logger, error) {
	level, err := parseLogLevel()
	if err != nil {
		return nil, err
	}

	var handler slog.Handler
	handlerOptions := &slog.HandlerOptions{
		Level: *level,
	}
	switch {
	case logFormat == "text":
		handler = slog.NewTextHandler(os.Stdout, handlerOptions)
	case logFormat == "json":
		handler = slog.NewJSONHandler(os.Stdout, handlerOptions)
	default:
		return nil, errors.New("unknown log format")
	}
	return slog.New(handler), nil
}
