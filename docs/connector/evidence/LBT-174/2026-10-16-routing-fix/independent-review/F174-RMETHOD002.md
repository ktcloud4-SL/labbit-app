# F174-RMETHOD002 — route 추가 결과 불명 시 finally 미진입

심각도: Low. 외부 `tmp/lbt174-routing-fix-20261016/local_probe_test.go:273`, `:292`, `:347`(관측 당시). 수정 전 root에 보고했다.

AB, Apt, intentional timeout helper에서 route add를 `try` 진입 전에 실행한다. 커널이 route를 추가했지만 Python 자식 명령이 timeout으로 응답을 잃으면 예외가 `finally` 밖에서 발생한다. 따라서 owned route 삭제와 부재 확인이 실행되지 않는다. 외부 진단 전용 disposable VM이며 VM teardown은 별도로 회수하므로 제품 수명주기 결함과 구분한다.

재현: 실제 Go generator에서 디코딩된 스크립트 본문을 사용하고 `bounded_run`에 timeout-after-apply를 주입했다. 실제 프로세스·네트워크 호출은 0이다. 세 경로 모두 add 1, delete 0, route retained로 FAIL했다. [probe](route_add_unknown_probe.py), [결과](route-add-unknown-before.json).

수정 설계: exact preexisting route 부재 확인 후 `try` 안에서 추가하고, `finally`에서 exact owned tuple 조회 후 존재할 때만 삭제·부재 확인한다. 추가가 응답 불명이었다고 무조건 replay하지 않는다. 원래 timeout/실패 결과는 유지한다. Apt metric40 default도 사전 충돌을 배제해야 한다. 필요한 회귀는 add timeout-after-apply, 정상 add, add reject/no route, cleanup lookup/delete 실패이며, 독립 후속 재검증은 아직 실행 전이다.

후속 상태: 위 문단은 수정 전 기록이다. root가 `owned_route` contextmanager로 변경한 실제 Go 생성 Python을 다시 추출해 독립 검증했다. [probe](refined_route_probe.py), [9경계 결과](route-add-unknown-after.json). 정상 add/body, add timeout-after-apply/no-apply, add reject, body 실패, preexisting, lookup 전/후 실패, delete 실패를 무해 stub로 확인했다. timeout-after-apply는 add1/delete1/최종부재이며 원래 timeout을 유지한다. 기존 route는 mutation0, lookup/delete 실패는 false PASS 없이 전파된다. 실제 프로세스·네트워크 mutation0. full helper Apt의 임시 route는 제거됐다. 실제 원격 marker/최종 VM 정리는 별도 actual 검증 경계로 남는다.
