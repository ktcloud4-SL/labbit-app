# F174-RMETHOD001 — 생성된 Apt 진단 스크립트 구문 오류

심각도: Low. 외부 `tmp/lbt174-routing-fix-20261016/local_probe_test.go:293`. 제품 경로와 별개인 외부 native Linux 검증 준비 helper다.

수정 전 `localAptCommand` raw Python의 top-level `print`에 선행 공백 한 칸이 있어 `IndentationError`가 발생했다. 원격 명령 실행 전 AST만으로 재현되며 native apt 단계는 시작할 수 없다. [수정 전 parse 결과](remote-script-parse-before.json). 수정 전 root에 보고했다.

필요한 회귀는 raw 문자열 일부가 아닌 실제 Go generator 결과의 base64 payload 전체를 디코딩하여 AST parse하는 것이다. 독립 [Go dumper](remote_python_dump_test.go.txt), [parser](parse_generated_python.py), [overlay](canonical-script-overlay.json)를 작성했다. 네트워크·원격 명령 실행 없이 Reachability, AB, Apt, intentional timeout, DHCP renew 양순서의 6개 스크립트가 PASS했다. [수정 후 결과](remote-script-parse-after.json). 구문 검사이며 sudo 권한·실제 패키지 설치·라우팅 성공을 보장하지 않는다.
