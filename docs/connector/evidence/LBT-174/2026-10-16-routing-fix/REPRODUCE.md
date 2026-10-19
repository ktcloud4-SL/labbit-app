# 재현과 다음 실행

현재 Git base0051d8d에서 code-inventory.json의 changed-source를 원래경로로 복원한다(.go.txt→.go). fullmanifest는LF정규화520파일을검증한다. 기존frozen275/123artifact는수정하지않는다.

WindowsGo1.27.1/GCCrace/Python3.12, 실제localOpenStack labbit-test, ubuntu/m1.small/sharednet1, SSHCIDR172.16.8.1/32, 보호clouds.yaml/SSHkey가필요하다. 민감입력은본package에없다. runtime관리망지원조건을먼저읽는다.

run의최종local_probe_test.go.txt→외부임시local_probe_test.go로복사하고 overlay.json의가상테스트경로/실제helper경로를새checkout에맞춘다. production overlay는없다. run_validation.py의ROOT/OUT을설정한다. build_sources.py source는고정Go/module/runtime/contractsmanifest와vendor포함archive를생성한다. officialtoolchain파일과SHA는linux-go-download.json을따른다. archive는Git에포함하지않는다.

- verify.py go/web/race(기본9pkg이므로최종11pkg은final_checks.py+LBT174_CHECK=race), run_db.py, check_format.py, final_checks.py로기본검증을재현한다. 계약validator는CI고정버전을설치한외부target디렉터리로준비한다.
- 관리자는검토된Set-LabbitVMnet8Dhcp.ps1 -Action Disable을명시적으로실행한다. 단일VMnet8scope/SHAguard가맞지않으면중단한다. VMnetDHCP서비스전체중지+비활성화이며미래VMwareDHCP에도영향이있다. -Action Restore는설정SHA를확인한후저장된시작유형/실행상태를복구한다. OpenStackAPI8.105가계속접근되는지확인한다. 관리자조치를이문서에서실행했다고주장하지않는다.
- 관리조치후 run_validation.py probe로exactfirst12/단일Labdefault/remotealarmcleanup/후속진단/routeAB/renew2순서를검사한다. helper full은nativeCI후실제rebootbootID+SSH/renew2순서/immutableResetg2후first12+renew2순서를포함한다. 실패시exactresourceledger로정리하고SG/keypairGET404를확인한다.
- native-independent/run_validation.py는routing/renew/reboot/Reset을제외한독립nativeCI다. apt단계만ownedLabmetric40route를사용하고finally부재확인한다. 이를라우팅수정성공이나전체fixturePASS로표시하지않는다. PGstartownership이있을때만DB0조회/stop한다.
- 보호env+필요한LinuxWorkspace/KTtooling testbinary를이전followup REPRODUCE대로준비한뒤 verify.py live-all로12fixture최종batch를실행한다. zero-match/skip은실fixturePASS가아니다.

이후원격main2703942와통합한별도소스로검증을갱신한다. 현재기록은0051기반이다. push/PR는사용자의다음명시지시전수행하지않는다.
