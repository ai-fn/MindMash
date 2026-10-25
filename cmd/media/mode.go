package main

import "fmt"

// mode selects the roles this process runs: the API (gRPC, REST, outbox relay, pending GC)
// and/or the image worker, which decodes uploads and so needs far more memory per replica.
type mode struct {
	api, worker bool
}

func parseMode(s string) (mode, error) {
	switch s {
	case "", "all":
		return mode{api: true, worker: true}, nil
	case "api":
		return mode{api: true}, nil
	case "worker":
		return mode{worker: true}, nil
	}
	return mode{}, fmt.Errorf(`media: MODE must be "api", "worker" or "all", got %q`, s)
}
