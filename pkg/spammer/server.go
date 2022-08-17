package spammer

import (
	"github.com/labstack/echo/v4"
)

type Server struct {
	Spammer *Spammer
}

func NewServer(spammer *Spammer, group *echo.Group) *Server {
	s := &Server{
		Spammer: spammer,
	}
	s.configureRoutes(group)

	return s
}
