# Backend Auth / Session Implementation Contract v0.1

이 문서는 SL-64의 Local Account 로그인과 Browser Session 구현 전에 필요한 세부 기준을 Git에서 고정합니다.

상위 정책은 D-11과 기능 명세, Browser HTTP 표면은 contracts/http/openapi.yaml, Physical Schema는 db/migrations/, 환경별 public origin 입력은 runtime/contract.yaml이 원본입니다.

## Password 저장과 검증

Local Account Password는 Argon2id PHC 문자열로 local_accounts.password_hash에 저장합니다.

v0.1 생성 baseline:
- Argon2id
- memory: 19 MiB
- iterations: 2
- parallelism: 1
- random salt: 16 bytes 이상
- derived key: 32 bytes

hash 문자열 안에 algorithm/version/parameter/salt/hash를 함께 저장해 향후 cost 상승을 허용합니다.

로그인 검증에서는 저장된 PHC parameter를 사용합니다. Password 원문이나 hash 전체를 로그/trace/Problem Details에 기록하지 않습니다. Unit test는 password hasher port/fake를 사용할 수 있고 PostgreSQL/Auth integration test에서는 실제 hash 1개 이상을 검증합니다.

## Browser Session token

새 Session token은 성공한 로그인마다 CSPRNG로 32 random bytes를 생성하고 base64url no-padding 같은 opaque 문자열로 Browser에 전달합니다.

- 기존 Cookie 값을 새 인증 Session ID로 재사용하지 않습니다.
- DB에는 raw token을 저장하지 않고 SHA-256(raw token) digest만 auth_sessions.token_hash에 저장합니다.
- raw token은 Set-Cookie와 이후 Browser Cookie request 외의 저장/로그/trace 대상이 아닙니다.

## Session 수명

v0.1 기준:
- server-side absolute lifetime: 8시간
- Browser Cookie: non-persistent session cookie
- sliding expiration: 사용하지 않음
- periodic token renewal: 사용하지 않음
- last_seen_at: 필요 시 관측/정리용으로 기록할 수 있지만 expires_at을 연장하지 않음

유효 Session은 최소 다음을 만족해야 합니다.

    token hash match
    AND revoked_at IS NULL
    AND expires_at > now()
    AND user.disabled_at IS NULL
    AND (
      password_changed_at IS NULL
      OR session.created_at >= password_changed_at
    )

만료/폐기/비활성/Password 변경 이전 Session으로 보호 API에 접근하면 401입니다. 가능하면 응답 시 stale Cookie도 제거합니다.

8시간 absolute lifetime은 MVP baseline이며 운영 사용 패턴을 확인한 뒤 idle/renewal policy가 필요하면 별도 계약 변경으로 추가합니다.

## Login

POST /api/v1/auth/login 성공 시:
1. username으로 Local Account를 조회합니다.
2. 계정이 있으면 저장된 PHC로, 없으면 고정된 dummy Argon2id PHC로 입력 Password를 반드시 1회 검증합니다.
3. 계정이 존재하고 Password가 일치한 경우에만 User disabled 여부를 확인합니다.
4. fresh random Session token을 생성합니다.
5. token hash와 8시간 expires_at을 DB에 저장합니다.
6. 새 __Host-labbit-session Cookie를 반환합니다.

존재하지 않는 username에서도 실제 계정과 같은 Argon2id baseline의 dummy hash 검증을 수행해 username 존재 여부가 Password hash 수행 유무만으로 드러나지 않게 합니다. dummy PHC는 애플리케이션 상수/설정으로 안전하게 관리하되 실제 사용자 Password나 계정에서 파생하지 않습니다.

같은 User가 다른 Browser에서 가진 Session은 로그인만으로 일괄 폐기하지 않습니다.

현재 Browser가 기존 유효 Session Cookie를 함께 보낸 상태에서 다시 로그인한 경우 성공 후 그 presented Session만 교체(revoke)하고 새 Session을 발급합니다.

잘못된 username/password는 계정 존재 여부를 구분하지 않는 동일한 401 의미를 사용합니다.

## Logout

POST /api/v1/auth/logout은 현재 Session의 revoked_at을 기록한 뒤 Browser Cookie를 제거합니다.

다른 Browser Session은 기본적으로 유지합니다.

## Cookie

production 및 production-like HTTPS:

    __Host-labbit-session=<opaque>
    Secure
    HttpOnly
    SameSite=Lax
    Path=/
    Domain 없음
    Max-Age/Expires 없음

Logout/invalid-session cleanup은 동일 scope로 Cookie를 비우고 Max-Age=0을 사용합니다.

__Host- prefix의 보안 속성을 개발 편의 때문에 production에서 약화하지 않습니다. 개발자 PC의 plain HTTP smoke는 production-like Cookie acceptance gate가 아니며 최종 Cookie 검증은 공유 Local HTTPS/AWS HTTPS에서 수행합니다.

Frontend는 Cookie를 직접 읽거나 localStorage/sessionStorage에 복사하지 않습니다.

## CSRF / Origin

MVP Browser API는 same-origin /api/v1을 기본으로 하고 credentialed cross-origin CORS를 제공하지 않습니다.

다음 unsafe method에는 Login을 포함해 source origin 검증을 적용합니다.
- POST
- PUT
- PATCH
- DELETE

검증 기준:
1. trusted target origin은 Runtime 설정 LABBIT_PUBLIC_ORIGIN에서 읽습니다.
2. Origin header가 있으면 scheme + host + port 전체가 target origin과 정확히 일치해야 합니다.
3. Origin이 없으면 Referer의 origin을 fallback으로 정확히 비교합니다.
4. 둘 다 없거나 malformed/null/mismatch이면 403 Problem Details로 거절합니다.
5. request Host / X-Forwarded-Host를 source of truth로 사용해 target origin을 동적으로 만들지 않습니다.

개발자 PC에서 Vite proxy를 사용할 때 LABBIT_PUBLIC_ORIGIN은 Browser가 실제로 보는 Vite origin을 사용합니다. 공유 Local/AWS는 실제 public HTTPS app origin을 사용합니다.

SameSite=Lax는 방어 계층 중 하나이며 Origin 검증을 대체하지 않습니다.

## 권한

Session 인증 성공은 Resource authorization 성공을 의미하지 않습니다.

- organizationRole=ADMIN은 Organization 관리 권한
- ClassMembership.role=INSTRUCTOR|STUDENT는 해당 Class 권한
- ADMIN이어도 대상 Class의 INSTRUCTOR Membership이 없으면 Class 운영 권한 없음

Class/API 권한은 UI가 아니라 Application use case에서 DB 관계를 사용해 검증합니다.

## SL-64 자동 테스트 최소 범위

- 정상 Password → fresh Session 발급
- 잘못된 username/password → 동일 401, 존재하지 않는 username도 dummy Argon2id 검증 수행
- raw Password/Session token 비로그
- Session token DB에는 digest만 저장
- 만료/revoked/disabled/password-changed 이전 Session → 401
- Login 시 기존 presented Session 교체
- Logout → 현재 Session revoke + Cookie clear
- 다른 Browser Session은 Logout으로 폐기되지 않음
- unsafe method Origin match → 허용
- Origin mismatch / missing Origin+Referer → 403
- /me, /classes, Class detail의 Organization/Membership 권한 분기
- 실제 PostgreSQL에서 Session/권한 query와 constraint 검증

## 이번 단계에서 하지 않는 것

- Self Signup / Invite / Join Code
- Self-service Password Reset
- MFA/OIDC/SAML
- Refresh token
- Redis/Valkey Session store
- 모든 Session을 강제 종료하는 사용자 UI
- idle/renewal timeout
- cross-origin credentialed API
- 최종 rate-limit/lockout 정책

## 참고

OWASP Session Management Cheat Sheet
https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html

OWASP CSRF Prevention Cheat Sheet
https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html

OWASP Password Storage Cheat Sheet
https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html

MDN Secure cookie configuration
https://developer.mozilla.org/en-US/docs/Web/Security/Practical_implementation_guides/Cookies
