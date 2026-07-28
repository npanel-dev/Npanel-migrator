package writer

import (
	"strings"
	"testing"
)

type openSourceCouponBuilder struct {
	count     int32
	usedCount int8
}

func (b *openSourceCouponBuilder) SetCount(value int32) *openSourceCouponBuilder {
	b.count = value
	return b
}

func (b *openSourceCouponBuilder) SetUsedCount(value int8) *openSourceCouponBuilder {
	b.usedCount = value
	return b
}

type commercialCouponBuilder struct {
	count     int64
	usedCount int64
}

func (b *commercialCouponBuilder) SetCount(value int64) *commercialCouponBuilder {
	b.count = value
	return b
}

func (b *commercialCouponBuilder) SetUsedCount(value int64) *commercialCouponBuilder {
	b.usedCount = value
	return b
}

func TestSetSignedIntegerBuilderFieldAcrossEditions(t *testing.T) {
	openSource := &openSourceCouponBuilder{}
	if err := setSignedIntegerBuilderField(openSource, "SetCount", 1000); err != nil {
		t.Fatal(err)
	}
	if err := setSignedIntegerBuilderField(openSource, "SetUsedCount", 100); err != nil {
		t.Fatal(err)
	}
	if openSource.count != 1000 || openSource.usedCount != 100 {
		t.Fatalf("unexpected open-source values: %+v", openSource)
	}

	commercial := &commercialCouponBuilder{}
	if err := setSignedIntegerBuilderField(commercial, "SetCount", 1<<40); err != nil {
		t.Fatal(err)
	}
	if err := setSignedIntegerBuilderField(commercial, "SetUsedCount", 1<<40); err != nil {
		t.Fatal(err)
	}
	if commercial.count != 1<<40 || commercial.usedCount != 1<<40 {
		t.Fatalf("unexpected commercial values: %+v", commercial)
	}
}

func TestSetSignedIntegerBuilderFieldRejectsOverflow(t *testing.T) {
	builder := &openSourceCouponBuilder{}
	err := setSignedIntegerBuilderField(builder, "SetUsedCount", 128)
	if err == nil || !strings.Contains(err.Error(), "超出") {
		t.Fatalf("error=%v, want overflow error", err)
	}
}
