package options

type ApiSettings struct {
	Offline     bool
	FetchBlocks bool
}

type ApiOption func(*ApiSettings) error

func ApiOptions(opts ...ApiOption) (*ApiSettings, error) {
	options := &ApiSettings{
		Offline:     false,
		FetchBlocks: true,
	}

	return ApiOptionsTo(options, opts...)
}

func ApiOptionsTo(options *ApiSettings, opts ...ApiOption) (*ApiSettings, error) {
	for _, opt := range opts {
		err := opt(options)
		if err != nil {
			return nil, err
		}
	}
	return options, nil
}

type apiOpts struct{}

var Api apiOpts

func (apiOpts) Offline(offline bool) ApiOption {
	return func(settings *ApiSettings) error {
		settings.Offline = offline
		return nil
	}
}

// FetchBlocks when set to false prevents api from fetching blocks from the
// network while allowing other services such as IPNS to still be online
func (apiOpts) FetchBlocks(fetch bool) ApiOption {
	return func(settings *ApiSettings) error {
		settings.FetchBlocks = fetch
		return nil
	}
}
