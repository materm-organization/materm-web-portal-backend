package bootstrap_test

import (
	"testing"

	"go-fiber/bootstrap"

	"github.com/stretchr/testify/assert"
)

func TestDBConnectionDSN(t *testing.T) {
	t.Run("full connection", func(t *testing.T) {
		c := bootstrap.DBConnection{
			Host: "db.local", Port: 5432, User: "u", Password: "p w'd\\", DBName: "app",
			SSLMode: "disable", Timezone: "Asia/Vientiane",
		}
		assert.Equal(t,
			`host='db.local' port=5432 user='u' password='p w\'d\\' dbname='app' sslmode='disable' TimeZone=Asia/Vientiane`,
			c.DSN())
	})

	t.Run("odd timezone is quoted", func(t *testing.T) {
		c := bootstrap.DBConnection{Host: "h", Port: 1, User: "u", Password: "p", DBName: "d", Timezone: "My Zone"}
		assert.Equal(t, `host='h' port=1 user='u' password='p' dbname='d' TimeZone='My Zone'`, c.DSN())
	})

	t.Run("device omits sslmode and timezone", func(t *testing.T) {
		c := bootstrap.DBConnection{Host: "h", Port: 1, User: "u", Password: "p", DBName: "d"}
		assert.Equal(t, `host='h' port=1 user='u' password='p' dbname='d'`, c.DSN())
	})
}
