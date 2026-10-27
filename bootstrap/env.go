package bootstrap

import (
	"log"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Env is the typed view of config.yaml. Keys use camelCase in the file; the
// mapstructure tags below must match them exactly.
type Env struct {
	Server    ServerConfig    `mapstructure:"server"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Redis     RedisConfig     `mapstructure:"redis"`
	JWT       JWTConfig       `mapstructure:"jwt"`
	Mailer    MailerConfig    `mapstructure:"mailer"`
	Bootstrap BootstrapConfig `mapstructure:"bootstrap"`
}

// ServerConfig holds HTTP server settings.
//
// ReadTimeout/WriteTimeout are decoded by viper's default hook, so write them
// as duration strings with a unit (e.g. "30s", "1m"). A bare number would be
// read as nanoseconds. Zero (blank) means "use the Fiber default / no limit".
type ServerConfig struct {
	Port         int           `mapstructure:"port"`
	ReadTimeout  time.Duration `mapstructure:"readTimeout"`
	WriteTimeout time.Duration `mapstructure:"writeTimeout"`
}

// DatabaseConfig holds the shared pool settings and the four named PostgreSQL
// connections. Only Admin is connected at startup; the others are parsed only.
type DatabaseConfig struct {
	Pool   PoolConfig   `mapstructure:"pool"`
	Admin  DBConnection `mapstructure:"admin"`
	Sell   DBConnection `mapstructure:"sell"`
	Erp    DBConnection `mapstructure:"erp"`
	Device DBConnection `mapstructure:"device"`
}

// PoolConfig maps to database/sql pool settings. A zero value leaves the
// corresponding database/sql default untouched.
type PoolConfig struct {
	MaxOpenConns           int `mapstructure:"maxOpenConns"`
	MaxIdleConns           int `mapstructure:"maxIdleConns"`
	ConnMaxLifetimeMinutes int `mapstructure:"connMaxLifetimeMinutes"`
	ConnMaxIdleTimeMinutes int `mapstructure:"connMaxIdleTimeMinutes"`
}

// DBConnection describes one PostgreSQL connection. SSLMode and Timezone are
// optional (the device connection does not define them) and are omitted from
// the DSN when empty.
type DBConnection struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
	DBName   string `mapstructure:"dbname"`
	SSLMode  string `mapstructure:"sslmode"`
	Timezone string `mapstructure:"timezone"`
}

type RedisConfig struct {
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	Username     string `mapstructure:"username"`
	Password     string `mapstructure:"password"`
	DB           int    `mapstructure:"db"`
	ClearOnStart bool   `mapstructure:"clearOnStart"`
}

type JWTConfig struct {
	Secret        string `mapstructure:"secret"`
	RefreshSecret string `mapstructure:"refreshSecret"`
}

// MailerConfig is parsed only; no feature uses it yet.
type MailerConfig struct {
	Host     string `mapstructure:"host"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	Port     int    `mapstructure:"port"`
	From     string `mapstructure:"from"`
}

// BootstrapConfig is parsed only; no feature uses it yet.
type BootstrapConfig struct {
	AdminUsername string `mapstructure:"adminUsername"`
	AdminPassword string `mapstructure:"adminPassword"`
}

func NewEnv() *Env {
	v := viper.New()

	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath("./")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := v.ReadInConfig(); err != nil {
		log.Fatal("Can't find the file .env : ", err)
	}

	var env Env
	if err := v.Unmarshal(&env); err != nil {
		log.Fatal("Environment can't be loaded: ", err)
	}
	log.Println("The App is configured to run on port:", env.Server.Port)
	return &env
}
