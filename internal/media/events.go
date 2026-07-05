package media

import (
	"encoding/json"
	"time"

	"github.com/mindmesh/mindmesh/internal/platform/events"
	"github.com/mindmesh/mindmesh/internal/platform/ids"
)

// Topic is the Kafka topic (== outbox aggregate_type) for media events, and the
// topic the image-processing worker subscribes to.
const Topic = "media"

// EventMediaUploaded is emitted once a blob is confirmed present in S3.
const EventMediaUploaded = "media.uploaded"

// uploadedPayload is the JSON body of a media.uploaded event. It carries enough
// for the worker to load the row and produce derived renditions.
type uploadedPayload struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	ProfileID string    `json:"profile_id"`
	Checksum  string    `json:"checksum"` // lowercase hex
	Kind      string    `json:"kind"`
	Mime      string    `json:"mime"`
	S3Key     string    `json:"s3_key"`
	USN       int64     `json:"usn"`
	Mtime     time.Time `json:"mtime"`
}

// mediaUploadedEntry builds the outbox entry for a confirmed media row. It is
// passed into the repo write so the event is appended in the same transaction.
func mediaUploadedEntry(m *Media) events.OutboxEntry {
	payload, err := json.Marshal(uploadedPayload{
		ID:        m.ID,
		UserID:    m.UserID,
		ProfileID: m.ProfileID,
		Checksum:  m.ChecksumHex(),
		Kind:      m.Kind,
		Mime:      m.Mime,
		S3Key:     m.S3Key,
		USN:       m.USN,
		Mtime:     m.Mtime,
	})
	if err != nil {
		panic("media events: marshal payload: " + err.Error())
	}
	return events.OutboxEntry{
		ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
		UserID:        m.UserID,
		AggregateType: Topic,
		AggregateID:   m.ID,
		EventType:     EventMediaUploaded,
		Payload:       payload,
		CreatedAt:     time.Now().UTC(),
	}
}
