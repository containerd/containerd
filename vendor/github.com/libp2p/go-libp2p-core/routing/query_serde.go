package routing

import (
	"encoding/json"

	"github.com/libp2p/go-libp2p-core/peer"
)

func (qe *QueryEvent) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"ID":        peer.IDB58Encode(qe.ID),
		"Type":      int(qe.Type),
		"Responses": qe.Responses,
		"Extra":     qe.Extra,
	})
}

func (qe *QueryEvent) UnmarshalJSON(b []byte) error {
	temp := struct {
		ID        string
		Type      int
		Responses []*peer.AddrInfo
		Extra     string
	}{}
	err := json.Unmarshal(b, &temp)
	if err != nil {
		return err
	}
	if len(temp.ID) > 0 {
		pid, err := peer.IDB58Decode(temp.ID)
		if err != nil {
			return err
		}
		qe.ID = pid
	}
	qe.Type = QueryEventType(temp.Type)
	qe.Responses = temp.Responses
	qe.Extra = temp.Extra
	return nil
}
