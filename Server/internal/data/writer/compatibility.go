package writer

import (
	"fmt"
	"reflect"
)

// setSignedIntegerBuilderField bridges Ent-generated setter signatures that
// differ between NPanel editions. For example, coupon count/used_count are
// int32/int8 in the open-source schema and int64 in the commercial schema.
func setSignedIntegerBuilderField(builder any, methodName string, value int64) error {
	method := reflect.ValueOf(builder).MethodByName(methodName)
	if !method.IsValid() {
		return fmt.Errorf("Ent builder 缺少 %s 方法", methodName)
	}
	methodType := method.Type()
	if methodType.NumIn() != 1 {
		return fmt.Errorf("Ent builder %s 参数数量异常: %d", methodName, methodType.NumIn())
	}
	argType := methodType.In(0)
	switch argType.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
	default:
		return fmt.Errorf("Ent builder %s 参数类型不受支持: %s", methodName, argType)
	}
	arg := reflect.New(argType).Elem()
	if arg.OverflowInt(value) {
		return fmt.Errorf("%s=%d 超出目标字段 %s 范围", methodName, value, argType)
	}
	arg.SetInt(value)
	method.Call([]reflect.Value{arg})
	return nil
}
