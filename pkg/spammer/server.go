package spammer

import (
	"github.com/labstack/echo/v4"
)

type SpammerServer struct {
	Spammer *Spammer
}

func NewSpammerServer(spammer *Spammer, group *echo.Group) *SpammerServer {
	s := &SpammerServer{
		Spammer: spammer,
	}
	s.configureRoutes(group)
	return s
}
