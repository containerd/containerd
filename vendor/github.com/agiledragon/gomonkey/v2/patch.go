package gomonkey

import (
	"fmt"
	"reflect"
	"syscall"
	"unsafe"

	"github.com/agiledragon/gomonkey/v2/creflect"
)

type Patches struct {
	originals    map[uintptr][]byte
	values       map[reflect.Value]reflect.Value
	valueHolders map[reflect.Value]reflect.Value
}

type Params []interface{}
type OutputCell struct {
	Values Params
	Times  int
}

func ApplyFunc(target, double interface{}) *Patches {
	return create().ApplyFunc(target, double)
}

func ApplyMethod(target interface{}, methodName string, double interface{}) *Patches {
	return create().ApplyMethod(target, methodName, double)
}

func ApplyMethodFunc(target interface{}, methodName string, doubleFunc interface{}) *Patches {
	return create().ApplyMethodFunc(target, methodName, doubleFunc)
}

func ApplyPrivateMethod(target interface{}, methodName string, double interface{}) *Patches {
	return create().ApplyPrivateMethod(target, methodName, double)
}

func ApplyGlobalVar(target, double interface{}) *Patches {
	return create().ApplyGlobalVar(target, double)
}

func ApplyFuncVar(target, double interface{}) *Patches {
	return create().ApplyFuncVar(target, double)
}

func ApplyFuncSeq(target interface{}, outputs []OutputCell) *Patches {
	return create().ApplyFuncSeq(target, outputs)
}

func ApplyMethodSeq(target interface{}, methodName string, outputs []OutputCell) *Patches {
	return create().ApplyMethodSeq(target, methodName, outputs)
}

func ApplyFuncVarSeq(target interface{}, outputs []OutputCell) *Patches {
	return create().ApplyFuncVarSeq(target, outputs)
}

func ApplyFuncReturn(target interface{}, output ...interface{}) *Patches {
	return create().ApplyFuncReturn(target, output...)
}

func ApplyMethodReturn(target interface{}, methodName string, output ...interface{}) *Patches {
	return create().ApplyMethodReturn(target, methodName, output...)
}

func ApplyFuncVarReturn(target interface{}, output ...interface{}) *Patches {
	return create().ApplyFuncVarReturn(target, output...)
}

func create() *Patches {
	return &Patches{originals: make(map[uintptr][]byte), values: make(map[reflect.Value]reflect.Value), valueHolders: make(map[reflect.Value]reflect.Value)}
}

func NewPatches() *Patches {
	return create()
}

func (this *Patches) ApplyFunc(target, double interface{}) *Patches {
	t := reflect.ValueOf(target)
	d := reflect.ValueOf(double)
	return this.ApplyCore(t, d)
}

func (this *Patches) ApplyMethod(target interface{}, methodName string, double interface{}) *Patches {
	m, ok := castRType(target).MethodByName(methodName)
	if !ok {
		panic("retrieve method by name failed")
	}
	d := reflect.ValueOf(double)
	return this.ApplyCore(m.Func, d)
}

func (this *Patches) ApplyMethodFunc(target interface{}, methodName string, doubleFunc interface{}) *Patches {
	m, ok := castRType(target).MethodByName(methodName)
	if !ok {
		panic("retrieve method by name failed")
	}
	d := funcToMethod(m.Type, doubleFunc)
	return this.ApplyCore(m.Func, d)
}

func (this *Patches) ApplyPrivateMethod(target interface{}, methodName string, double interface{}) *Patches {
	m, ok := creflect.MethodByName(castRType(target), methodName)
	if !ok {
		panic("retrieve method by name failed")
	}
	d := reflect.ValueOf(double)
	return this.ApplyCoreOnlyForPrivateMethod(m, d)
}

func (this *Patches) ApplyGlobalVar(target, double interface{}) *Patches {
	t := reflect.ValueOf(target)
	if t.Type().Kind() != reflect.Ptr {
		panic("target is not a pointer")
	}

	this.values[t] = reflect.ValueOf(t.Elem().Interface())
	d := reflect.ValueOf(double)
	t.Elem().Set(d)
	return this
}

func (this *Patches) ApplyFuncVar(target, double interface{}) *Patches {
	t := reflect.ValueOf(target)
	d := reflect.ValueOf(double)
	if t.Type().Kind() != reflect.Ptr {
		panic("target is not a pointer")
	}
	this.check(t.Elem(), d)
	return this.ApplyGlobalVar(target, double)
}

func (this *Patches) ApplyFuncSeq(target interface{}, outputs []OutputCell) *Patches {
	funcType := reflect.TypeOf(target)
	t := reflect.ValueOf(target)
	d := getDoubleFunc(funcType, outputs)
	return this.ApplyCore(t, d)
}

func (this *Patches) ApplyMethodSeq(target interface{}, methodName string, outputs []OutputCell) *Patches {
	m, ok := castRType(target).MethodByName(methodName)
	if !ok {
		panic("retrieve method by name failed")
	}
	d := getDoubleFunc(m.Type, outputs)
	return this.ApplyCore(m.Func, d)
}

func (this *Patches) ApplyFuncVarSeq(target interface{}, outputs []OutputCell) *Patches {
	t := reflect.ValueOf(target)
	if t.Type().Kind() != reflect.Ptr {
		panic("target is not a pointer")
	}
	if t.Elem().Kind() != reflect.Func {
		panic("target is not a func")
	}

	funcType := reflect.TypeOf(target).Elem()
	double := getDoubleFunc(funcType, outputs).Interface()
	return this.ApplyGlobalVar(target, double)
}

func (this *Patches) ApplyFuncReturn(target interface{}, returns ...interface{}) *Patches {
	funcType := reflect.TypeOf(target)
	t := reflect.ValueOf(target)
	outputs := []OutputCell{{Values: returns, Times: -1}}
	d := getDoubleFunc(funcType, outputs)
	return this.ApplyCore(t, d)
}

func (this *Patches) ApplyMethodReturn(target interface{}, methodName string, returns ...interface{}) *Patches {
	m, ok := reflect.TypeOf(target).MethodByName(methodName)
	if !ok {
		panic("retrieve method by name failed")
	}

	outputs := []OutputCell{{Values: returns, Times: -1}}
	d := getDoubleFunc(m.Type, outputs)
	return this.ApplyCore(m.Func, d)
}

func (this *Patches) ApplyFuncVarReturn(target interface{}, returns ...interface{}) *Patches {
	t := reflect.ValueOf(target)
	if t.Type().Kind() != reflect.Ptr {
		panic("target is not a pointer")
	}
	if t.Elem().Kind() != reflect.Func {
		panic("target is not a func")
	}

	funcType := reflect.TypeOf(target).Elem()
	outputs := []OutputCell{{Values: returns, Times: -1}}
	double := getDoubleFunc(funcType, outputs).Interface()
	return this.ApplyGlobalVar(target, double)
}

func (this *Patches) Reset() {
	for target, bytes := range this.originals {
		modifyBinary(target, bytes)
		delete(this.originals, target)
	}

	for target, variable := range this.values {
		target.Elem().Set(variable)
	}
}

func (this *Patches) ApplyCore(target, double reflect.Value) *Patches {
	this.check(target, double)
	assTarget := *(*uintptr)(getPointer(target))
	if _, ok := this.originals[assTarget]; ok {
		panic("patch has been existed")
	}

	this.valueHolders[double] = double
	original := replace(assTarget, uintptr(getPointer(double)))
	this.originals[assTarget] = original
	return this
}

func (this *Patches) ApplyCoreOnlyForPrivateMethod(target unsafe.Pointer, double reflect.Value) *Patches {
	if double.Kind() != reflect.Func {
		panic("double is not a func")
	}
	assTarget := *(*uintptr)(target)
	if _, ok := this.originals[assTarget]; ok {
		panic("patch has been existed")
	}
	this.valueHolders[double] = double
	original := replace(assTarget, uintptr(getPointer(double)))
	this.originals[assTarget] = original
	return this
}

func (this *Patches) check(target, double reflect.Value) {
	if target.Kind() != reflect.Func {
		panic("target is not a func")
	}

	if double.Kind() != reflect.Func {
		panic("double is not a func")
	}

	if target.Type() != double.Type() {
		panic(fmt.Sprintf("target type(%s) and double type(%s) are different", target.Type(), double.Type()))
	}
}

func replace(target, double uintptr) []byte {
	code := buildJmpDirective(double)
	bytes := entryAddress(target, len(code))
	original := make([]byte, len(bytes))
	copy(original, bytes)
	modifyBinary(target, code)
	return original
}

func getDoubleFunc(funcType reflect.Type, outputs []OutputCell) reflect.Value {
	if funcType.NumOut() != len(outputs[0].Values) {
		panic(fmt.Sprintf("func type has %v return values, but only %v values provided as double",
			funcType.NumOut(), len(outputs[0].Values)))
	}

	needReturn := false
	slice := make([]Params, 0)
	for _, output := range outputs {
		if output.Times == -1 {
			needReturn = true
			slice = []Params{output.Values}
			break
		}
		t := 0
		if output.Times <= 1 {
			t = 1
		} else {
			t = output.Times
		}
		for j := 0; j < t; j++ {
			slice = append(slice, output.Values)
		}
	}

	i := 0
	lenOutputs := len(slice)
	return reflect.MakeFunc(funcType, func(_ []reflect.Value) []reflect.Value {
		if needReturn {
			return GetResultValues(funcType, slice[0]...)
		}
		if i < lenOutputs {
			i++
			return GetResultValues(funcType, slice[i-1]...)
		}
		panic("double seq is less than call seq")
	})
}

func GetResultValues(funcType reflect.Type, results ...interface{}) []reflect.Value {
	var resultValues []reflect.Value
	for i, r := range results {
		var resultValue reflect.Value
		if r == nil {
			resultValue = reflect.Zero(funcType.Out(i))
		} else {
			v := reflect.New(funcType.Out(i))
			v.Elem().Set(reflect.ValueOf(r))
			resultValue = v.Elem()
		}
		resultValues = append(resultValues, resultValue)
	}
	return resultValues
}

type funcValue struct {
	_ uintptr
	p unsafe.Pointer
}

func getPointer(v reflect.Value) unsafe.Pointer {
	return (*funcValue)(unsafe.Pointer(&v)).p
}

func entryAddress(p uintptr, l int) []byte {
	return *(*[]byte)(unsafe.Pointer(&reflect.SliceHeader{Data: p, Len: l, Cap: l}))
}

func pageStart(ptr uintptr) uintptr {
	return ptr & ^(uintptr(syscall.Getpagesize() - 1))
}

func funcToMethod(funcType reflect.Type, doubleFunc interface{}) reflect.Value {
	rf := reflect.TypeOf(doubleFunc)
	if rf.Kind() != reflect.Func {
		panic("doubleFunc is not a func")
	}
	vf := reflect.ValueOf(doubleFunc)
	return reflect.MakeFunc(funcType, func(in []reflect.Value) []reflect.Value {
		if funcType.IsVariadic() {
			return vf.CallSlice(in[1:])
		} else {
			return vf.Call(in[1:])
		}
	})
}

func castRType(val interface{}) reflect.Type {
	if rTypeVal, ok := val.(reflect.Type); ok {
		return rTypeVal
	}
	return reflect.TypeOf(val)
}
