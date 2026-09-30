package jsonnum

import (
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

func TestPositiveInteger(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		// JSON Schema 2020-12에서 소수부가 0인 표기도 integer다.
		{"1", true},
		{"10", true},
		{"123456789012345678901234567890", true},
		{"1.0", true},
		{"1e2", true},
		{"1E2", true},
		{"1e+2", true},
		{"100e-2", true},
		{"0.1e1", true},
		{"10.50e1", true},
		{"9223372036854775808", true}, // int64를 넘어도 Schema 위반이 아니다.
		{"1e999999999", true},
		{"1e999999999999999999999", true},
		{"123e-2", false}, // 1.23
		// 정수가 아니거나 1 미만이다.
		{"0", false},
		{"-1", false},
		{"-0", false},
		{"0.0", false},
		{"0e5", false},
		{"0e999999999999999999999", false},
		{"1.5", false},
		{"0.5", false},
		{"1e-1", false},
		{"1e-999999999999999999999", false},
		{"15e-1", false},
		// JSON number가 아니다.
		{"", false},
		{" 1", false},
		{"1 ", false},
		{"+1", false},
		{".5", false},
		{"1.", false},
		{"1e", false},
		{"e1", false},
		{"00", false},
		{"01", false},
		{"0x10", false},
		{"1_0", false},
		{"NaN", false},
		{"Infinity", false},
		{"null", false},
		{"true", false},
		{`"1"`, false},
		{`[1]`, false},
	}
	for _, tt := range tests {
		if got := PositiveInteger([]byte(tt.raw)); got != tt.want {
			t.Errorf("PositiveInteger(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
	if PositiveInteger(nil) {
		t.Error("PositiveInteger(nil) = true: 없는 field는 number가 아니다")
	}
}

func TestInt64(t *testing.T) {
	tests := []struct {
		raw     string
		value   int64
		valid   bool
		inRange bool
	}{
		{raw: "0", value: 0, valid: true, inRange: true},
		{raw: "-0", value: 0, valid: true, inRange: true},
		{raw: "7", value: 7, valid: true, inRange: true},
		{raw: "-7", value: -7, valid: true, inRange: true},
		{raw: "1e2", value: 100, valid: true, inRange: true},
		{raw: "120e-1", value: 12, valid: true, inRange: true},
		{raw: "1.0", value: 1, valid: true, inRange: true},
		{raw: "9223372036854775807", value: 9223372036854775807, valid: true, inRange: true},
		{raw: "-9223372036854775808", value: -9223372036854775808, valid: true, inRange: true},
		{raw: "9223372036854775808", valid: true, inRange: false},
		{raw: "-9223372036854775809", valid: true, inRange: false},
		{raw: "1e30", valid: true, inRange: false},
		{raw: "1e999999999999999999999", valid: true, inRange: false},
		{raw: "1.5", valid: false},
		{raw: "12e-1", valid: false},
		{raw: "1e-999999999999999999999", valid: false},
		{raw: "abc", valid: false},
		{raw: `"7"`, valid: false},
		{raw: "null", valid: false},
		{raw: "", valid: false},
	}
	for _, tt := range tests {
		value, valid, inRange := Int64([]byte(tt.raw))
		if valid != tt.valid || inRange != tt.inRange {
			t.Errorf("Int64(%q) = valid %v inRange %v, want %v %v", tt.raw, valid, inRange, tt.valid, tt.inRange)
			continue
		}
		if valid && inRange && value != tt.value {
			t.Errorf("Int64(%q) = %d, want %d", tt.raw, value, tt.value)
		}
		if !(valid && inRange) && value != 0 {
			t.Errorf("Int64(%q) = %d, want 0 when not representable", tt.raw, value)
		}
	}
}

// 임의의 JSON number 문법을 만들어 math/big의 정확한 유리수 계산과 대조한다. 지수는 oracle이 정확히 계산할 수 있는 범위로 제한한다.
func TestAgainstBigRatOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	digits := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteByte(byte('0' + rng.Intn(10)))
		}
		return b.String()
	}

	for i := 0; i < 50000; i++ {
		var b strings.Builder
		if rng.Intn(6) == 0 {
			b.WriteByte('-')
		}
		if rng.Intn(4) == 0 {
			b.WriteString("0")
		} else {
			b.WriteByte(byte('1' + rng.Intn(9)))
			b.WriteString(digits(rng.Intn(12)))
		}
		if rng.Intn(2) == 0 {
			b.WriteByte('.')
			b.WriteString(digits(1 + rng.Intn(8)))
		}
		if rng.Intn(2) == 0 {
			b.WriteString([]string{"e", "E"}[rng.Intn(2)])
			b.WriteString([]string{"", "+", "-"}[rng.Intn(3)])
			b.WriteString(fmt.Sprint(rng.Intn(25)))
		}
		raw := b.String()

		rat, ok := new(big.Rat).SetString(raw)
		if !ok {
			t.Fatalf("oracle이 %q를 해석하지 못함", raw)
		}
		wantPositiveInt := rat.IsInt() && rat.Sign() > 0
		if got := PositiveInteger([]byte(raw)); got != wantPositiveInt {
			t.Fatalf("PositiveInteger(%q) = %v, oracle %v (%s)", raw, got, wantPositiveInt, rat.RatString())
		}

		value, valid, inRange := Int64([]byte(raw))
		if valid != rat.IsInt() {
			t.Fatalf("Int64(%q) valid = %v, oracle integer %v", raw, valid, rat.IsInt())
		}
		if valid {
			num := rat.Num()
			wantInRange := num.IsInt64()
			if inRange != wantInRange {
				t.Fatalf("Int64(%q) inRange = %v, oracle %v", raw, inRange, wantInRange)
			}
			if inRange && value != num.Int64() {
				t.Fatalf("Int64(%q) = %d, oracle %d", raw, value, num.Int64())
			}
		}
	}
}

// 지수가 매우 커도 입력 길이에 비례하는 시간에 끝나야 한다(큰 수를 만들어 계산하지 않는다).
func TestHugeExponentIsLinearTime(t *testing.T) {
	raw := "1e" + strings.Repeat("9", 1<<20)
	if !PositiveInteger([]byte(raw)) {
		t.Fatal("1e<huge>는 Schema-valid integer여야 한다")
	}
	if _, valid, inRange := Int64([]byte(raw)); !valid || inRange {
		t.Fatalf("Int64(1e<huge>) = valid %v inRange %v, want valid, out of range", valid, inRange)
	}
	if PositiveInteger([]byte("1e-" + strings.Repeat("9", 1<<20))) {
		t.Fatal("1e-<huge>는 정수가 아니다")
	}
}
