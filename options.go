package lrcd

import (
	"context"
	"errors"
	"io"

	lrcpb "github.com/rachel-mp4/lrcproto/gen/go"
)

type InitChanMsg struct {
	lrcpb.Event_Init
	*string
}

type MediaInitChanMsg struct {
	lrcpb.Event_Mediainit
	*string
}

type options struct {
	uri           string
	secret        string
	welcome       *string
	writer        *io.Writer
	verbose       bool
	pubChan       chan PubEvent
	initChan      chan InitChanMsg
	mediainitChan chan MediaInitChanMsg
	initialID     *uint32
	resolver      func(externalID string, ctx context.Context) *string
}

type Option func(option *options) error

func WithResolver(f func(externalID string, ctx context.Context) *string) Option {
	return func(options *options) error {
		if f == nil {
			return errors.New("resolver must be non nil")
		}
		options.resolver = f
		return nil
	}
}

func WithWelcome(welcome string) Option {
	return func(options *options) error {
		options.welcome = &welcome
		return nil
	}
}

func WithInitialID(id uint32) Option {
	return func(options *options) error {
		options.initialID = &id
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

func WithInitChannel(initChan chan InitChanMsg) Option {
	return func(options *options) error {
		if initChan == nil {
			return errors.New("must provide a channel")
		}
		options.initChan = initChan
		return nil
	}
}

func WithMediainitChannel(mediainitChan chan MediaInitChanMsg) Option {
	return func(options *options) error {
		if mediainitChan == nil {
			return errors.New("must provide a channel")
		}
		options.mediainitChan = mediainitChan
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

func WithServerURIAndSecret(uri string, secret string) Option {
	return func(options *options) error {
		options.secret = secret
		options.uri = uri
		return nil
	}
}
