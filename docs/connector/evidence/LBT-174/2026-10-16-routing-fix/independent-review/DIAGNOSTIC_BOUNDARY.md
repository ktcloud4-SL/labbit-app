# 전용 SSH 진단 helper의 취소 경계

`integrationDiagnosticSSH`는 phase 소유 SSH transport를 context 취소 시 닫고 `session.Run`이 반환할 때까지 동기 대기한다. stdout/stderr를 mutex writer로 공유하며 Run 반환 후 출력 소비가 가능하다. 실제 호출은 phase별 새 client와 파일 writer를 사용한다.

독립 실제 loopback SSH fault fixture에서 session Open reply hold, exec reply hold, exit-status 이후 output EOF hold, deadline hold, 정상 stdout/stderr/exit의 5개 프로토콜 경계를 검증했다. checked-in 10개 cancellation subtest와 함께 `-race -count=5`가 exit 0으로 PASS했다. [외부 fixture](diagnostic_boundary_test.go.txt), [overlay](diagnostic-overlay.json), [로그](diagnostic-protocol-race.log.gz). 실제 SSH 프로토콜을 사용하지만 원격 peer는 로컬 test server이며 cloud 실행이 아니다.

따라서 이전과 같은 cancel 후 session.Run 잔류 문제는 timer 값 확대만으로 가린 것이 아니다. 전용 transport 종료와 호출 goroutine join이 검증됐다. 그러나 SSH transport Close는 원격 Python의 finally 실행·자식 프로세스 종료·route 부재를 증명하지 않는다. 원격 자식 timeout, remote alarm, exact cleanup marker 및 VM 최종 부재는 별도 실제 검증이 필요하다. supplied output writer 자체가 영구 블로킹하는 경계는 이 helper의 검증 범위 밖이며 현재 file writer 호출에서 관측한 결함은 없다.

진단 Python AST 6개 PASS는 구문 검증이다. route add 응답 불명 시 정리 누락은 별도 [F174-RMETHOD002](F174-RMETHOD002.md)로 보고했다. canonical DHCP 옵션 저장 성공과 실제 guest 적용은 별도이며, 실제 renew 검증 실패가 열려 있어 전체 검증 완료나 PR 가능 판정을 내리지 않는다.
