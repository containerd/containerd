[![GoDoc](https://godoc.org/github.com/xeipuuv/gojsonschema?status.svg)](https://godoc.org/github.com/xeipuuv/gojsonschema)
[![Build Status](https://travis-ci.org/xeipuuv/gojsonschema.svg)](https://travis-ci.org/xeipuuv/gojsonschema)

# gojsonschema

## Description

An implementation of JSON Schema for the Go  programming language. Supports draft-04, draft-06 and draft-07.

References :

* http://json-schema.org
* http://json-schema.org/latest/json-schema-core.html
* http://json-schema.org/latest/json-schema-validation.html

## Installation

```
go get github.com/xeipuuv/gojsonschema
```

Dependencies :
* [github.com/xeipuuv/gojsonpointer](https://github.com/xeipuuv/gojsonpointer)
* [github.com/xeipuuv/gojsonreference](https://github.com/xeipuuv/gojsonreference)
* [github.com/stretchr/testify/assert](https://github.com/stretchr/testify#assert-package)

## Usage

### Example

```go

package main

import (
    "fmt"
    "github.com/xeipuuv/gojsonschema"
)

func main() {

    schemaLoader := gojsonschema.NewReferenceLoader("file:///home/me/schema.json")
    documentLoader := gojsonschema.NewReferenceLoader("file:///home/me/document.json")

    result, err := gojsonschema.Validate(schemaLoader, documentLoader)
    if err != nil {
        panic(err.Error())
    }

    if result.Valid() {
        fmt.Printf("The document is valid\n")
    } else {
        fmt.Printf("The document is not valid. see errors :\n")
        for _, desc := range result.Errors() {
            fmt.Printf("- %s\n", desc)
        }
    }

}


```

#### Loaders

There are various ways to load your JSON data.
In order to load your schemas and documents,
first declare an appropriate loader :

* Web / HTTP, using a reference :

```go
loader := gojsonschema.NewReferenceLoader("http://www.some_host.com/schema.json")
```

* Local file, using a reference :

```go
loader := gojsonschema.NewReferenceLoader("file:///home/me/schema.json")
```

References use the URI scheme, the prefix (file://) and a full path to the file are required.

* JSON strings :

```go
loader := gojsonschema.NewStringLoader(`{"type": "string"}`)
```

* Custom Go types :

```go
m := map[string]interface{}{"type": "string"}
loader := gojsonschema.NewGoLoader(m)
```

And

```go
type Root struct {
	Users []User `json:"users"`
}

type User struct {
	Name string `json:"name"`
}

...

data := Root{}
data.Users = append(data.Users, User{"John"})
data.Users = append(data.Users, User{"Sophia"})
data.Users = append(data.Users, User{"Bill"})

loader := gojsonschema.NewGoLoader(data)
```

#### Validation

Once the loaders are set, validation is easy :

```go
result, err := gojsonschema.Validate(schemaLoader, documentLoader)
```

Alternatively, you might want to load a schema only once and process to multiple validations :

```go
schema, err := gojsonschema.NewSchema(schemaLoader)
...
result1, err := schema.Validate(documentLoader1)
...
result2, err := schema.Validate(documentLoader2)
...
// etc ...
```

To check the result :

```go
    if result.Valid() {
    	fmt.Printf("The document is valid\n")
    } else {
        fmt.Printf("The document is not valid. see errors :\n")
        for _, err := range result.Errors() {
        	// Err implements the ResultError interface
            fmt.Printf("- %s\n", err)
        }
    }
```

## Working with Errors

The library handles string error codes which you can customize by creating your own gojsonschema.locale and setting it
```go
gojsonschema.Locale = YourCustomLocale{}
```

However, each error contains additional contextual information. 

Newer versions of `gojsonschema` may have new additional errors, so code that uses a custom locale will need to be updated when this happens.

**err.Type()**: *string* Returns the "type" of error that occurred. Note you can also type check. See below

Note: An error of RequiredType has an err.Type() return value of "required"

    "required": RequiredError
    "invalid_type": InvalidTypeError
    "number_any_of": NumberAnyOfError
    "number_one_of": NumberOneOfError
    "number_all_of": NumberAllOfError
    "number_not": NumberNotError
    "missing_dependency": MissingDependencyError
    "internal": InternalError
    "const": ConstEror
    "enum": EnumError
    "array_no_additional_items": ArrayNoAdditionalItemsError
    "array_min_items": ArrayMinItemsError
    "array_max_items": ArrayMaxItemsError
    "unique": ItemsMustBeUniqueError
    "contains" : ArrayContainsError
    "array_min_properties": ArrayMinPropertiesError
    "array_max_properties": ArrayMaxPropertiesError
    "additional_property_not_allowed": AdditionalPropertyNotAllowedError
    "invalid_property_pattern": InvalidPropertyPatternError
    "invalid_property_name":  InvalidPropertyNameError
    "string_gte": StringLengthGTEError
    "string_lte": StringLengthLTEError
    "pattern": DoesNotMatchPatternError
    "multiple_of": MultipleOfError
    "number_gte": NumberGTEError
    "number_gt": NumberGTError
    "number_lte": NumberLTEError
    "number_lt": NumberLTError

**err.Value()**: *interface{}* Returns the value given

**err.Context()**: *gojsonschema.JsonContext* Returns the context. This has a String() method that will print something like this: (root).firstName

**err.Field()**: *string* Returns the fieldname in the format firstName, or for embedded properties, person.firstName. This returns the same as the String() method on *err.Context()* but removes the (root). prefix.

**err.Description()**: *string* The error description. This is based on the locale you are using. See the beginning of this section for overwriting the locale with a custom implementation.

**err.DescriptionFormat()**: *string* The error description format. This is relevant if you are adding custom validation errors afterwards to the result.

**err.Details()**: *gojsonschema.ErrorDetails* Returns a map[string]interface{} of additional error details specific to the error. For example, GTE errors will have a "min" value, LTE will have a "max" value. See errors.go for a full description of all the error details. Every error always contains a "field" key that holds the value of *err.Field()*

Note in most cases, the err.Details() will be used to generate replacement strings in your locales, and not used directly. These strings follow the text/template format i.e.
```
{{.field}} must be greater than or equal to {{.min}}
```

The library allows you to specify custom template functions, should you require more complex error message handling.
```go
gojsonschema.ErrorTemplateFuncs = map[string]interface{}{
	"allcaps": func(s string) string {
		return strings.ToUpper(s)
	},
}
```

Given the above definition, you can use the custom function `"allcaps"` in your localization templates:
```
{{allcaps .field}} must be greater than or equal to {{.min}}
```

The above error message would then be rendered with the `field` value in capital letters. For example:
```
"PASSWORD must be greater than or equal to 8"
```

Learn more about what types of template functions you can use in `ErrorTemplateFuncs` by referring to Go's [text/template FuncMap](https://golang.org/pkg/text/template/#FuncMap) type.

## Formats
JSON Schema allows for optional "format" property to validate instances against well-known formats. gojsonschema ships with all of the formats defined in the spec that you can use like this:
````json
{"type": "string", "format": "email"}
````
Available formats: date-time, hostname, email, ipv4, ipv6, uri, uri-reference, uuid, regex. Some of the new formats in draft-06 and draft-07 are not yet implemented.

For repetitive or more complex formats, you can create custom format checkers and add them to gojsonschema like this:

```go
// Define the format checker
type RoleFormatChecker struct {}

// Ensure it meets the gojsonschema.FormatChecker interface
func (f RoleFormatChecker) IsFormat(input interface{}) bool {

    asString, ok := input.(string)
    if ok == false {
        return false
    }

    return strings.HasPrefix("ROLE_", asString)
}

// Add it to the library
gojsonschema.FormatCheckers.Add("role", RoleFormatChecker{})
````

Now to use in your json schema:
````json
{"type": "string", "format": "role"}
````

Another example would be to check if the provided integer matches an id on database:

JSON schema:
```json
{"type": "integer", "format": "ValidUserId"}
```

```go
// Define the format checker
type ValidUserIdFormatChecker struct {}

// Ensure it meets the gojsonschema.FormatChecker interface
func (f ValidUserIdFormatChecker) IsFormat(input interface{}) bool {

    asFloat64, ok := input.(float64) // Numbers are always float64 here
    if ok == false {
        return false
    }

    // XXX
    // do the magic on the database looking for the int(asFloat64)

    return true
}

// Add it to the library
gojsonschema.FormatCheckers.Add("ValidUserId", ValidUserIdFormatChecker{})
````

## Additional custom validation
After the validation has run and you have the results, you may add additional
errors using `Result.AddError`. This is useful to maintain the same format within the resultset instead
of having to add special exceptions for your own errors. Below is an example.

```go
type AnswerInvalidError struct {
    gojsonschema.ResultErrorFields
}

func newAnswerInvalidError(context *gojsonschema.JsonContext, value interface{}, details gojsonschema.ErrorDetails) *AnswerInvalidError {
    err := AnswerInvalidError{}
    err.SetContext(context)
    err.SetType("custom_invalid_error")
    // it is important to use SetDescriptionFormat() as this is used to call SetDescription() after it has been parsed
    // using the description of err will be overridden by this.
    err.SetDescriptionFormat("Answer to the Ultimate Question of Life, the Universe, and Everything is {{.answer}}")
    err.SetValue(value)
    err.SetDetails(details)

    return &err
}

func main() {
    // ...
    schema, err := gojsonschema.NewSchema(schemaLoader)
    result, err := gojsonschema.Validate(schemaLoader, documentLoader)

    if true { // some validation
        jsonContext := gojsonschema.NewJsonContext("question", nil)
        errDetail := gojsonschema.ErrorDetails{
            "answer": 42,
        }
        result.AddError(
            newAnswerInvalidError(
                gojsonschema.NewJsonContext("answer", jsonContext),
                52,
                errDetail,
            ),
            errDetail,
        )
    }

    return result, err

}
```

This is especially useful if you want to add validation beyond what the
json schema drafts can provide such business specific logic.

## Uses

gojsonschema uses the following test suite :

https://github.com/json-schema/JSON-Schema-Test-Suite
