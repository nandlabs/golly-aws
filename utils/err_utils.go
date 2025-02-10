package utils

import "fmt"

func UnsupportedOperation(name string) error {
	return fmt.Errorf("operation %s is not supported", name)
}
