package containerd

import specs "github.com/opencontainers/runtime-spec/specs-go"

func createDefaultSpec() (*specs.Spec, error) {
	return &specs.Spec{
		Version: specs.Version,
		Root:    &specs.Root{},
		Process: &specs.Process{
			Cwd: `C:\`,
			ConsoleSize: &specs.Box{
				Width:  80,
				Height: 20,
			},
		},
		Windows: &specs.Windows{
			IgnoreFlushesDuringBoot: true,
			Network: &specs.WindowsNetwork{
				AllowUnqualifiedDNSQuery: true,
			},
		},
	}, nil
}
