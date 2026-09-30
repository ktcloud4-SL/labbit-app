// Package jsonnum은 JSON Schema 2020-12의 integer 규칙으로 JSON number를 판정한다.
//
// JSON Schema에서 1.0, 1e2처럼 소수부가 0인 표기도 integer다. Schema에 maximum이 없으면 int64를 넘는 값도 Schema 위반이
// 아니다. 이 package는 값을 계산하지 않고 숫자 문법만 lexical하게 판정하므로 1e999999999 같은 입력도 입력 길이에 비례한
// 시간에 처리하며, Schema에 없는 상한을 만들지 않는다. 표준 library만 사용한다.
package jsonnum

import (
	"strconv"
	"strings"
)

// number는 JSON number를 부호, 유효숫자열(앞뒤 0 없음), 10의 지수로 분해한 값이다. 값 = ±digits × 10^exp다.
type number struct {
	negative bool
	zero     bool
	// huge이면 지수가 너무 커서 exp를 표현할 수 없다. 이때 값은 0이 아닌 정수이며 int64 범위를 넘는다.
	huge   bool
	digits string
	exp    int64
}

// parse는 raw를 JSON number 문법에 맞게 분해한다. 숫자가 아니면 ok가 false다.
func parse(raw []byte) (n number, isInteger, ok bool) {
	digitsAt := func(i int) int {
		count := 0
		for i+count < len(raw) && raw[i+count] >= '0' && raw[i+count] <= '9' {
			count++
		}
		return count
	}

	i := 0
	if i < len(raw) && raw[i] == '-' {
		n.negative = true
		i++
	}
	intLen := digitsAt(i)
	if intLen == 0 || (intLen > 1 && raw[i] == '0') { // number가 아닌 값과 앞자리 0도 여기서 거절한다.
		return number{}, false, false
	}
	intPart := string(raw[i : i+intLen])
	i += intLen

	var fracPart string
	if i < len(raw) && raw[i] == '.' {
		i++
		fracLen := digitsAt(i)
		if fracLen == 0 {
			return number{}, false, false
		}
		fracPart = string(raw[i : i+fracLen])
		i += fracLen
	}

	var expDigits string
	expNegative := false
	if i < len(raw) && (raw[i] == 'e' || raw[i] == 'E') {
		i++
		if i < len(raw) && (raw[i] == '+' || raw[i] == '-') {
			expNegative = raw[i] == '-'
			i++
		}
		expLen := digitsAt(i)
		if expLen == 0 {
			return number{}, false, false
		}
		expDigits = string(raw[i : i+expLen])
		i += expLen
	}
	if i != len(raw) {
		return number{}, false, false
	}

	all := strings.TrimLeft(intPart+fracPart, "0")
	if all == "" {
		n.zero = true
		return n, true, true
	}
	mantissa := strings.TrimRight(all, "0")
	shift := int64(len(fracPart) - (len(all) - len(mantissa)))
	n.digits = mantissa

	expDigits = strings.TrimLeft(expDigits, "0")
	if len(expDigits) > 18 {
		// 지수의 크기가 shift(입력 길이 이하)보다 항상 크므로 부호만 본다.
		if expNegative {
			return n, false, true
		}
		n.huge = true
		return n, true, true
	}
	var exp int64
	if expDigits != "" {
		exp, _ = strconv.ParseInt(expDigits, 10, 64)
	}
	if expNegative {
		exp = -exp
	}
	n.exp = exp - shift
	return n, n.exp >= 0, true
}

// PositiveInteger는 raw가 1 이상인 정수 값의 JSON number일 때만 true다(Schema의 integer, minimum 1).
// string, bool, null, 비어 있는 raw는 number가 아니므로 false다.
func PositiveInteger(raw []byte) bool {
	n, isInteger, ok := parse(raw)
	return ok && isInteger && !n.zero && !n.negative
}

// Int64는 정수 JSON number의 값을 반환한다. 정수가 아니면 valid가 false이고,
// 정수지만 int64로 표현할 수 없으면 inRange가 false이며 value는 0이다.
func Int64(raw []byte) (value int64, valid, inRange bool) {
	n, isInteger, ok := parse(raw)
	if !ok || !isInteger {
		return 0, false, false
	}
	if n.zero {
		return 0, true, true
	}
	if n.huge || int64(len(n.digits))+n.exp > 19 {
		return 0, true, false
	}
	text := n.digits + strings.Repeat("0", int(n.exp))
	if n.negative {
		text = "-" + text
	}
	parsed, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, true, false
	}
	return parsed, true, true
}
