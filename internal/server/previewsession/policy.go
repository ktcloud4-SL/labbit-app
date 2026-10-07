package previewsession

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ManagementPort는 SSH 관리 port다. 허용 목록에 있어도 Preview 대상으로 항상 거절한다.
const ManagementPort = 22

// ErrNoAllowedPorts는 허용 port 목록이 비어 있거나 설정되지 않았음이다. 설정이 없으면 어떤 port도 승인하지 않는다(fail closed).
var ErrNoAllowedPorts = errors.New("허용 port가 하나도 없습니다")

// Policy는 PreviewSession이 승인할 수 있는 Workspace VM application port의 Backend 명시적 허용 목록이다(LABBIT_PREVIEW_ALLOWED_PORTS).
//
// 이 목록이 trust root다. 숫자 범위로 자동 승인하지 않고, 코드에 기본 port를 두지 않는다. 3000, 80, 5000도 목록에 있을 때만 승인한다.
// process 전체의 global 정책이며 LabSpec별 정책이 아니다. zero value는 아무것도 승인하지 않는다.
type Policy struct {
	ports map[int]struct{}
}

// ParseAllowedPorts는 쉼표로 구분한 10진수 port 목록을 읽는다.
//
//   - 비어 있거나 공백뿐이면 ErrNoAllowedPorts다(fail closed).
//   - 각 항목은 1~65535의 canonical 10진수여야 한다. 앞의 0, 부호, 소수, 16진수, 범위 표기("3000-3010"), 빈 항목은 형식 오류다.
//     범위 표기를 허용하지 않으므로 숫자 범위가 자동 승인되는 일이 없다.
//   - 중복은 하나로 정규화한다.
//   - 22는 형식상 받아들이지만 Approve가 항상 거절한다.
func ParseAllowedPorts(raw string) (Policy, error) {
	if strings.TrimSpace(raw) == "" {
		return Policy{}, ErrNoAllowedPorts
	}
	ports := make(map[int]struct{})
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		port, err := parsePort(item)
		if err != nil {
			return Policy{}, fmt.Errorf("허용 port 목록의 항목이 올바르지 않습니다: %w", err)
		}
		ports[port] = struct{}{}
	}
	return Policy{ports: ports}, nil
}

func parsePort(item string) (int, error) {
	if item == "" {
		return 0, errors.New("빈 항목")
	}
	for _, c := range item {
		if c < '0' || c > '9' {
			return 0, errors.New("10진수 port만 허용합니다(범위 표기 불가)")
		}
	}
	if item[0] == '0' {
		return 0, errors.New("앞에 0이 있거나 0입니다")
	}
	n, err := strconv.Atoi(item)
	if err != nil || n < 1 || n > 65535 {
		return 0, errors.New("1~65535여야 합니다")
	}
	return n, nil
}

// Approve는 port를 PreviewSession의 대상으로 승인할 수 있는지 판정한다. 승인하지 않으면 ErrPortNotAllowed다.
// 허용 목록에 있는 정확한 값만 승인하며 SSH 관리 port(22)는 목록에 있어도 거절한다.
func (p Policy) Approve(port int) error {
	if port == ManagementPort {
		return ErrPortNotAllowed
	}
	if _, ok := p.ports[port]; !ok {
		return ErrPortNotAllowed
	}
	return nil
}

// Ports는 허용 목록의 port를 오름차순으로 반환한다(SSH 관리 port 제외, 승인할 수 있는 값만). 진단 log용이다.
func (p Policy) Ports() []int {
	out := make([]int, 0, len(p.ports))
	for port := range p.ports {
		if port != ManagementPort {
			out = append(out, port)
		}
	}
	slices.Sort(out)
	return out
}
