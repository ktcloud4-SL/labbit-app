BEGIN;

-- D-25: API 응답 종료 후에도 Worker가 원본 요청의 Trace Context를 복원한다.
-- 기존 Migration을 재작성하지 않는 additive 변경이다. 기존 row는 NULL을 유지한다.
-- 유효성/길이 제한은 Application의 W3C 정상화 단계에서 검사하고 잘못된 값은 NULL로 저장한다.
-- Trace metadata 때문에 업무 INSERT/UPDATE가 실패하지 않도록 필수/고유 제약을 추가하지 않는다.
ALTER TABLE operations
    ADD COLUMN traceparent text NULL,
    ADD COLUMN tracestate text NULL;

COMMENT ON COLUMN operations.traceparent IS
    'Optional W3C context serialized from the SaaS span at Operation creation; not an authorization or idempotency key.';
COMMENT ON COLUMN operations.tracestate IS
    'Optional validated W3C tracestate accompanying traceparent; no credentials, user content, or baggage.';

COMMIT;
