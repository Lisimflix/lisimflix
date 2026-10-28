package contracts

import "testing"

var userDataFixtures = map[string]func() any{
	"progress-seek.json":      func() any { return &ProgressEvent{} },
	"progress-duplicate.json": func() any { return &ProgressResult{} },
	"watched-request.json":    func() any { return &WatchedRequest{} },
}

func TestUserDataFixturesRoundTrip(t *testing.T) { roundTripFixtures(t, userDataFixtures) }

func TestUserDataEnumsMatchOpenAPI(t *testing.T) {
	checkEnum(t, "Watched", "origin", WatchedAuto, WatchedManual, WatchedImport)
	checkEnum(t, "ProgressEvent", "kind", ProgressTick, ProgressPause, ProgressSeek, ProgressStop)
	checkEnum(t, "ProgressResult", "reason", ProgressDuplicate, ProgressStale)
}
