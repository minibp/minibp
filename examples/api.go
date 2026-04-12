// api.go - Go API package
package api

import "fmt"

type Service struct {
	Name string
}

func (s *Service) Start() error {
	fmt.Printf("Starting service: %s\n", s.Name)
	return nil
}

func (s *Service) Stop() error {
	fmt.Printf("Stopping service: %s\n", s.Name)
	return nil
}
