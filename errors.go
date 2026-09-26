package lrcd

import "errors"

var (
	ErrIDDNE         = errors.New("provided id does not exist")
	ErrIDDone        = errors.New("provided id has been published")
	ErrServerStarted = errors.New("cannot start already started server")
	ErrServerStopped = errors.New("cannot stop already stopped server")
)
