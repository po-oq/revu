package app

const DefaultMaxUploadBytes int64 = 10 * 1024 * 1024

type Config struct {
	Addr           string
	DataDir        string
	MaxUploadBytes int64
}

func DefaultConfig() Config {
	return Config{
		Addr:           "0.0.0.0:8080",
		DataDir:        "data",
		MaxUploadBytes: DefaultMaxUploadBytes,
	}
}
