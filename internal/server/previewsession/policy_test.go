package previewsession

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func mustPolicy(t *testing.T, raw string) Policy {
	t.Helper()
	p, err := ParseAllowedPorts(raw)
	if err != nil {
		t.Fatalf("ParseAllowedPorts(%q) error = %v", raw, err)
	}
	return p
}

// 허용 목록에 있는 정확한 값만 승인한다. 3000은 test 입력일 뿐 기본값이 아니다.
func TestApproveOnlyExactlyListedPorts(t *testing.T) {
	p := mustPolicy(t, "3000")
	if err := p.Approve(3000); err != nil {
		t.Fatalf("Approve(3000) error = %v", err)
	}
	for _, port := range []int{1, 22, 80, 2999, 3001, 5000, 5173, 8080, 65535} {
		if err := p.Approve(port); !errors.Is(err, ErrPortNotAllowed) {
			t.Errorf("Approve(%d) error = %v, want ErrPortNotAllowed", port, err)
		}
	}
}

// 3000이 목록에 없으면 3000을 승인하지 않는다(어떤 port도 product default가 아니다).
func TestPortThreeThousandIsNotADefault(t *testing.T) {
	p := mustPolicy(t, "8080")
	if err := p.Approve(3000); !errors.Is(err, ErrPortNotAllowed) {
		t.Fatalf("Approve(3000) error = %v, want ErrPortNotAllowed", err)
	}
	if err := p.Approve(8080); err != nil {
		t.Fatalf("Approve(8080) error = %v", err)
	}
}

// 80과 5000도 숫자만 보고 일괄 금지하지 않는다. 명시적으로 설정돼 있을 때만 승인한다.
func TestPortsEightyAndFiveThousandAreApprovedOnlyWhenListed(t *testing.T) {
	for _, port := range []int{80, 5000} {
		if err := mustPolicy(t, "80,5000").Approve(port); err != nil {
			t.Errorf("목록에 있는 %d를 승인하지 않음: %v", port, err)
		}
		if err := mustPolicy(t, "3000").Approve(port); !errors.Is(err, ErrPortNotAllowed) {
			t.Errorf("목록에 없는 %d를 승인함: %v", port, err)
		}
	}
	// 80은 특권 port지만 숫자로 판단하지 않는다. 80만 있는 목록은 5000을 승인하지 않는다.
	if err := mustPolicy(t, "80").Approve(5000); !errors.Is(err, ErrPortNotAllowed) {
		t.Errorf("80만 있는 목록이 5000을 승인함: %v", err)
	}
}

// SSH 관리 port(22)는 목록에 있어도 항상 거절한다.
func TestManagementPortIsAlwaysRejectedEvenWhenConfigured(t *testing.T) {
	p := mustPolicy(t, "22,3000")
	if err := p.Approve(22); !errors.Is(err, ErrPortNotAllowed) {
		t.Fatalf("Approve(22) error = %v, want ErrPortNotAllowed", err)
	}
	if err := p.Approve(3000); err != nil {
		t.Fatalf("Approve(3000) error = %v", err)
	}
	if got := p.Ports(); !slices.Equal(got, []int{3000}) {
		t.Fatalf("Ports() = %v, want 22가 빠진 [3000]", got)
	}
	// 22만 설정해도 형식상 유효하지만 아무것도 승인하지 않는다.
	if err := mustPolicy(t, "22").Approve(22); !errors.Is(err, ErrPortNotAllowed) {
		t.Fatalf("22만 있는 목록 Approve(22) error = %v", err)
	}
}

// 설정이 없거나 비어 있으면 fail closed다.
func TestUnsetOrEmptyAllowListFailsClosed(t *testing.T) {
	for _, raw := range []string{"", " ", "\t\n", "   "} {
		if _, err := ParseAllowedPorts(raw); !errors.Is(err, ErrNoAllowedPorts) {
			t.Errorf("ParseAllowedPorts(%q) error = %v, want ErrNoAllowedPorts", raw, err)
		}
	}
	var zero Policy
	for _, port := range []int{1, 22, 80, 3000, 5000, 8080, 65535} {
		if err := zero.Approve(port); !errors.Is(err, ErrPortNotAllowed) {
			t.Errorf("zero Policy.Approve(%d) error = %v, want ErrPortNotAllowed", port, err)
		}
	}
	if len(zero.Ports()) != 0 {
		t.Errorf("zero Policy.Ports() = %v", zero.Ports())
	}
}

func TestInvalidAllowListConfigIsRejected(t *testing.T) {
	invalid := map[string]string{
		"빈 항목(중간)":      "3000,,80",
		"빈 항목(끝 쉼표)":    "3000,",
		"빈 항목(앞 쉼표)":    ",3000",
		"쉼표만":           ",",
		"범위 표기":         "3000-3010",
		"범위 표기(공백)":     "3000 - 3010",
		"wildcard":      "*",
		"wildcard 접미":   "30*",
		"0":             "0",
		"앞의 0":          "03000",
		"음수":            "-80",
		"양수 부호":         "+80",
		"소수":            "3000.0",
		"지수":            "3e3",
		"16진수":          "0x1F90",
		"65536":         "65536",
		"아주 큰 수":        "99999999999999999999",
		"문자":            "http",
		"공백이 들어간 숫자":    "30 00",
		"한 항목만 잘못됨":     "3000,abc,80",
		"세미콜론 구분자":      "3000;80",
		"URL":           "http://localhost:3000",
		"port 목록처럼 보이는": "[3000]",
	}
	for name, raw := range invalid {
		if _, err := ParseAllowedPorts(raw); err == nil {
			t.Errorf("%s: ParseAllowedPorts(%q)가 오류 없이 통과함", name, raw)
		}
	}
}

func TestAllowListNormalizesDuplicatesAndWhitespace(t *testing.T) {
	p := mustPolicy(t, " 8080 , 3000,3000,\t80 ,8080 ")
	if got := p.Ports(); !slices.Equal(got, []int{80, 3000, 8080}) {
		t.Fatalf("Ports() = %v, want 정규화한 [80 3000 8080]", got)
	}
	for _, port := range []int{80, 3000, 8080} {
		if err := p.Approve(port); err != nil {
			t.Errorf("Approve(%d) error = %v", port, err)
		}
	}
}

// 숫자 범위가 자동 승인되지 않는다. 목록의 양 끝 사이에 있는 값도 정확히 목록에 있어야 한다.
func TestArbitraryNumericRangesAreNeverApproved(t *testing.T) {
	p := mustPolicy(t, "3000,3010")
	for port := 3001; port <= 3009; port++ {
		if err := p.Approve(port); !errors.Is(err, ErrPortNotAllowed) {
			t.Errorf("Approve(%d) error = %v: 두 허용 port 사이의 값이 승인됨", port, err)
		}
	}
	for _, port := range []int{0, -1, 65536, 70000} {
		if err := p.Approve(port); !errors.Is(err, ErrPortNotAllowed) {
			t.Errorf("Approve(%d) error = %v", port, err)
		}
	}
	if err := p.Approve(3010); err != nil {
		t.Errorf("Approve(3010) error = %v", err)
	}
}

// 설정 오류 문구는 입력 원문을 되풀이하지 않는다(설정 값이 log에 남아도 값이 새지 않게 한다).
func TestParseErrorsDoNotEchoTheirInput(t *testing.T) {
	_, err := ParseAllowedPorts("3000,secret-looking-value")
	if err == nil {
		t.Fatal("형식 오류를 받아들임")
	}
	if strings.Contains(err.Error(), "secret-looking-value") {
		t.Fatalf("오류가 입력 원문을 되풀이함: %v", err)
	}
}
