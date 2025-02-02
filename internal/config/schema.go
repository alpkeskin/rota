package config

type Config struct {
	ProxyFile   string            `yaml:"proxy_file"`
	FileWatch   bool              `yaml:"file_watch"`
	Proxy       ProxyConfig       `yaml:"proxy"`
	Api         ApiConfig         `yaml:"api"`
	Healthcheck HealthcheckConfig `yaml:"healthcheck"`
	Logging     LoggingConfig     `yaml:"logging"`
}

type ProxyConfig struct {
	Port           int                       `yaml:"port"`
	Authentication ProxyAuthenticationConfig `yaml:"authentication"`
	Rotation       ProxyRotationConfig       `yaml:"rotation"`
	RateLimit      RateLimitConfig           `yaml:"rate_limit"`
}

type ProxyAuthenticationConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type ProxyRotationConfig struct {
	Method             string          `yaml:"method"`
	TimeBased          TimeBasedConfig `yaml:"time_based"`
	RemoveUnhealthy    bool            `yaml:"remove_unhealthy"`
	Fallback           bool            `yaml:"fallback"`
	FallbackMaxRetries int             `yaml:"fallback_max_retries"`
	Timeout            int             `yaml:"timeout"`
	Retries            int             `yaml:"retries"`
}

type RateLimitConfig struct {
	Enabled     bool `yaml:"enabled"`
	Interval    int  `yaml:"interval"`
	MaxRequests int  `yaml:"max_requests"`
}

type TimeBasedConfig struct {
	Interval int `yaml:"interval"`
}

type ApiConfig struct {
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port"`
}

type HealthcheckConfig struct {
	Output  HealthcheckOutputConfig `yaml:"output"`
	Timeout int                     `yaml:"timeout"`
	Workers int                     `yaml:"workers"`
	URL     string                  `yaml:"url"`
	Status  int                     `yaml:"status"`
	Headers []string                `yaml:"headers"`
}

type HealthcheckOutputConfig struct {
	Method string `yaml:"method"`
	File   string `yaml:"file"`
}

type LoggingConfig struct {
	Stdout bool   `yaml:"stdout"`
	File   string `yaml:"file"`
	Level  string `yaml:"level"`
}
