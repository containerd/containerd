package peer

import (
	"encoding/json"

	ma "github.com/multiformats/go-multiaddr"
)

// Helper struct for decoding as we can't unmarshal into an interface (Multiaddr).
type addrInfoJson struct {
	ID    ID
	Addrs []string
}

func (pi AddrInfo) MarshalJSON() ([]byte, error) {
	addrs := make([]string, len(pi.Addrs))
	for i, addr := range pi.Addrs {
		addrs[i] = addr.String()
	}
	return json.Marshal(&addrInfoJson{
		ID:    pi.ID,
		Addrs: addrs,
	})
}

func (pi *AddrInfo) UnmarshalJSON(b []byte) error {
	var data addrInfoJson
	if err := json.Unmarshal(b, &data); err != nil {
		return err
	}
	addrs := make([]ma.Multiaddr, len(data.Addrs))
	for i, addr := range data.Addrs {
		maddr, err := ma.NewMultiaddr(addr)
		if err != nil {
			return err
		}
		addrs[i] = maddr
	}

	pi.ID = data.ID
	pi.Addrs = addrs
	return nil
}
