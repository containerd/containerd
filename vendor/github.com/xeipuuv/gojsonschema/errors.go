package gojsonschema

import (
	"bytes"
	"sync"
	"text/template"
)

var errorTemplates errorTemplate = errorTemplate{template.New("errors-new"), sync.RWMutex{}}

// template.Template is not thread-safe for writing, so some locking is done
// sync.RWMutex is used for efficiently locking when new templates are created
type errorTemplate struct {
	*template.Template
	sync.RWMutex
}

type (
	// RequiredError. ErrorDetails: property string
	RequiredError struct {
		ResultErrorFields
	}

	// InvalidTypeError. ErrorDetails: expected, given
	InvalidTypeError struct {
		ResultErrorFields
	}

	// NumberAnyOfError. ErrorDetails: -
	NumberAnyOfError struct {
		ResultErrorFields
	}

	// NumberOneOfError. ErrorDetails: -
	NumberOneOfError struct {
		ResultErrorFields
	}

	// NumberAllOfError. ErrorDetails: -
	NumberAllOfError struct {
		ResultErrorFields
	}

	// NumberNotError. ErrorDetails: -
	NumberNotError struct {
		ResultErrorFields
	}

	// MissingDependencyError. ErrorDetails: dependency
	MissingDependencyError struct {
		ResultErrorFields
	}

	// InternalError. ErrorDetails: error
	InternalError struct {
		ResultErrorFields
	}

	// ConstError. ErrorDetails: allowed
	ConstError struct {
		ResultErrorFields
	}

	// EnumError. ErrorDetails: allowed
	EnumError struct {
		ResultErrorFields
	}

	// ArrayNoAdditionalItemsError. ErrorDetails: -
	ArrayNoAdditionalItemsError struct {
		ResultErrorFields
	}

	// ArrayMinItemsError. ErrorDetails: min
	ArrayMinItemsError struct {
		ResultErrorFields
	}

	// ArrayMaxItemsError. ErrorDetails: max
	ArrayMaxItemsError struct {
		ResultErrorFields
	}

	// ItemsMustBeUniqueError. ErrorDetails: type
	ItemsMustBeUniqueError struct {
		ResultErrorFields
	}

	// ArrayContainsError. ErrorDetails:
	ArrayContainsError struct {
		ResultErrorFields
	}

	// ArrayMinPropertiesError. ErrorDetails: min
	ArrayMinPropertiesError struct {
		ResultErrorFields
	}

	// ArrayMaxPropertiesError. ErrorDetails: max
	ArrayMaxPropertiesError struct {
		ResultErrorFields
	}

	// AdditionalPropertyNotAllowedError. ErrorDetails: property
	AdditionalPropertyNotAllowedError struct {
		ResultErrorFields
	}

	// InvalidPropertyPatternError. ErrorDetails: property, pattern
	InvalidPropertyPatternError struct {
		ResultErrorFields
	}

	// InvalidPopertyNameError. ErrorDetails: property
	InvalidPropertyNameError struct {
		ResultErrorFields
	}

	// StringLengthGTEError. ErrorDetails: min
	StringLengthGTEError struct {
		ResultErrorFields
	}

	// StringLengthLTEError. ErrorDetails: max
	StringLengthLTEError struct {
		ResultErrorFields
	}

	// DoesNotMatchPatternError. ErrorDetails: pattern
	DoesNotMatchPatternError struct {
		ResultErrorFields
	}

	// DoesNotMatchFormatError. ErrorDetails: format
	DoesNotMatchFormatError struct {
		ResultErrorFields
	}

	// MultipleOfError. ErrorDetails: multiple
	MultipleOfError struct {
		ResultErrorFields
	}

	// NumberGTEError. ErrorDetails: min
	NumberGTEError struct {
		ResultErrorFields
	}

	// NumberGTError. ErrorDetails: min
	NumberGTError struct {
		ResultErrorFields
	}

	// NumberLTEError. ErrorDetails: max
	NumberLTEError struct {
		ResultErrorFields
	}

	// NumberLTError. ErrorDetails: max
	NumberLTError struct {
		ResultErrorFields
	}

	// ConditionThenError. ErrorDetails: -
	ConditionThenError struct {
		ResultErrorFields
	}

	// ConditionElseError. ErrorDetails: -
	ConditionElseError struct {
		ResultErrorFields
	}
)

// newError takes a ResultError type and sets the type, context, description, details, value, and field
func newError(err ResultError, context *JsonContext, value interface{}, locale locale, details ErrorDetails) {
	var t string
	var d string
	switch err.(type) {
	case *RequiredError:
		t = "required"
		d = locale.Required()
	case *InvalidTypeError:
		t = "invalid_type"
		d = locale.InvalidType()
	case *NumberAnyOfError:
		t = "number_any_of"
		d = locale.NumberAnyOf()
	case *NumberOneOfError:
		t = "number_one_of"
		d = locale.NumberOneOf()
	case *NumberAllOfError:
		t = "number_all_of"
		d = locale.NumberAllOf()
	case *NumberNotError:
		t = "number_not"
		d = locale.NumberNot()
	case *MissingDependencyError:
		t = "missing_dependency"
		d = locale.MissingDependency()
	case *InternalError:
		t = "internal"
		d = locale.Internal()
	case *ConstError:
		t = "const"
		d = locale.Const()
	case *EnumError:
		t = "enum"
		d = locale.Enum()
	case *ArrayNoAdditionalItemsError:
		t = "array_no_additional_items"
		d = locale.ArrayNoAdditionalItems()
	case *ArrayMinItemsError:
		t = "array_min_items"
		d = locale.ArrayMinItems()
	case *ArrayMaxItemsError:
		t = "array_max_items"
		d = locale.ArrayMaxItems()
	case *ItemsMustBeUniqueError:
		t = "unique"
		d = locale.Unique()
	case *ArrayContainsError:
		t = "contains"
		d = locale.ArrayContains()
	case *ArrayMinPropertiesError:
		t = "array_min_properties"
		d = locale.ArrayMinProperties()
	case *ArrayMaxPropertiesError:
		t = "array_max_properties"
		d = locale.ArrayMaxProperties()
	case *AdditionalPropertyNotAllowedError:
		t = "additional_property_not_allowed"
		d = locale.AdditionalPropertyNotAllowed()
	case *InvalidPropertyPatternError:
		t = "invalid_property_pattern"
		d = locale.InvalidPropertyPattern()
	case *InvalidPropertyNameError:
		t = "invalid_property_name"
		d = locale.InvalidPropertyName()
	case *StringLengthGTEError:
		t = "string_gte"
		d = locale.StringGTE()
	case *StringLengthLTEError:
		t = "string_lte"
		d = locale.StringLTE()
	case *DoesNotMatchPatternError:
		t = "pattern"
		d = locale.DoesNotMatchPattern()
	case *DoesNotMatchFormatError:
		t = "format"
		d = locale.DoesNotMatchFormat()
	case *MultipleOfError:
		t = "multiple_of"
		d = locale.MultipleOf()
	case *NumberGTEError:
		t = "number_gte"
		d = locale.NumberGTE()
	case *NumberGTError:
		t = "number_gt"
		d = locale.NumberGT()
	case *NumberLTEError:
		t = "number_lte"
		d = locale.NumberLTE()
	case *NumberLTError:
		t = "number_lt"
		d = locale.NumberLT()
	case *ConditionThenError:
		t = "condition_then"
		d = locale.ConditionThen()
	case *ConditionElseError:
		t = "condition_else"
		d = locale.ConditionElse()
	}

	err.SetType(t)
	err.SetContext(context)
	err.SetValue(value)
	err.SetDetails(details)
	err.SetDescriptionFormat(d)
	details["field"] = err.Field()

	if _, exists := details["context"]; !exists && context != nil {
		details["context"] = context.String()
	}

	err.SetDescription(formatErrorDescription(err.DescriptionFormat(), details))
}

// formatErrorDescription takes a string in the default text/template
// format and converts it to a string with replacements. The fields come
// from the ErrorDetails struct and vary for each type of error.
func formatErrorDescription(s string, details ErrorDetails) string {

	var tpl *template.Template
	var descrAsBuffer bytes.Buffer
	var err error

	errorTemplates.RLock()
	tpl = errorTemplates.Lookup(s)
	errorTemplates.RUnlock()

	if tpl == nil {
		errorTemplates.Lock()
		tpl = errorTemplates.New(s)

		if ErrorTemplateFuncs != nil {
			tpl.Funcs(ErrorTemplateFuncs)
		}

		tpl, err = tpl.Parse(s)
		errorTemplates.Unlock()

		if err != nil {
			return err.Error()
		}
	}

	err = tpl.Execute(&descrAsBuffer, details)
	if err != nil {
		return err.Error()
	}

	return descrAsBuffer.String()
}
