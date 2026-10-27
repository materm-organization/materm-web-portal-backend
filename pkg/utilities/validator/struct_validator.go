package validator

import (
	"fmt"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

type ErrorResponse struct {
	FailedField string `json:"failed_field"`
	Tag         string `json:"tag"`
	Value       string `json:"value"`
}

var validate *validator.Validate

func init() {
	validate = validator.New()
	// field names in errors are the JSON names clients know (the path of a field error is a JSON Pointer)
	validate.RegisterTagNameFunc(func(f reflect.StructField) string {
		name := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		if name == "" {
			return f.Name
		}
		return name
	})
}

// ValidateStruct runs go-playground/validator on v using the "validate" struct tags.
// Returns nil if valid, or an error describing the first validation failure.
func ValidateStruct(v any) error {
	err := validate.Struct(v)
	if err == nil {
		return nil
	}
	if errs, ok := err.(validator.ValidationErrors); ok && len(errs) > 0 {
		e := errs[0]
		return fmt.Errorf("%s: %s", e.Field(), e.Tag())
	}
	return err
}

func NewValidateStruct(myStruct interface{}) string {
	var errorX []*ErrorResponse
	validate := validator.New()
	err := validate.Struct(myStruct)
	if err != nil {
		for _, err := range err.(validator.ValidationErrors) {
			var element ErrorResponse
			var tag string
			switch err.Tag() {
			case "required":
				tag = "is"
			default:
				tag = "must be"
			}
			var errorMessage = err.Field()
			errorMessage = strings.ToLower(errorMessage)
			element.FailedField = errorMessage + " " + tag + " " + err.Tag() + " " + err.Param()
			element.Tag = err.Tag()
			element.Value = err.Param()
			errorX = append(errorX, &element)
		}

	}
	if errorX != nil {
		logs.Error(errorX)
		return errorX[0].FailedField
	}
	return ""
}
