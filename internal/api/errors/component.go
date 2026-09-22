package errors

import (
	"errors"
	"fmt"
)

var ErrComponentDSNotExist = errors.New("component does not exist")

func NewErrComponentDSNotExist(componentID int) error {
	return fmt.Errorf("%w, component_id: %d", ErrComponentDSNotExist, componentID)
}

var ErrComponentExist = errors.New("component already exists")
var ErrComponentInvalidFormat = errors.New("component invalid format")
var ErrComponentAttrInvalidFormat = errors.New("component attribute has invalid format")
var ErrComponentTypeAttrMissing = errors.New("component attribute type is missing or invalid")
var ErrComponentCategoryAttrMissing = errors.New("component attribute category is missing or invalid")
