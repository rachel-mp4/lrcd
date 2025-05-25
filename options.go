package lrcd

import (
	"errors"
	"io"
	"unicode/utf8"

	lrcpb "github.com/rachel-mp4/lrcproto/gen/go"
)

type options struct {
	welcome  *string
	writer   *io.Writer
	verbose  bool
	pubChan  chan PubEvent
	initChan chan lrcpb.Event_Init
}

type Option func(option *options) error

func WithWelcome(welcome string) Option {
	return func(options *options) error {
		if utf8.RuneCountInString(welcome) > 50 {
			return errors.New("welcome must be at most 50 runes")
		}
		options.welcome = &welcome
		return nil
	}
}

func WithLogging(w io.Writer, verbose bool) Option {
	return func(options *options) error {
		if w == nil {
			return errors.New("must provide a writer to log to")
		}
		options.writer = &w
		options.verbose = verbose
		return nil
	}
}

func WithInitChannel(initChan chan lrcpb.Event_Init) Option {
	return func(options *options) error {
		if initChan == nil {
			return errors.New("must provide a channel")
		}
		options.initChan = initChan
		return nil
	}
}

func WithPubChannel(pubChan chan PubEvent) Option {
	return func(options *options) error {
		if pubChan == nil {
			return errors.New("must provide a channel")
		}
		options.pubChan = pubChan
		return nil
	}
}
