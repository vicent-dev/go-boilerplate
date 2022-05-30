package app

import (
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func (s *server) database() {
	var err error

	connection := s.c.Mysql.User + ":" + s.c.Mysql.Pwd + "@tcp(" + s.c.Mysql.Host + ":" + s.c.Mysql.Port + ")/" + s.c.Mysql.Name + "?parseTime=true"

	s.db, err = gorm.Open(mysql.Open(connection), &gorm.Config{})

	if err != nil {
		panic(err)
	}
}
