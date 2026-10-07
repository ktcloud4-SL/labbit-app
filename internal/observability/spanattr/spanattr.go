package spanattr

import "go.opentelemetry.io/otel/attribute"

// Span attribute key다. 제품 package가 공유하는 이름이며 SDK를 import하지 않는다(trace API의 attribute 타입만 쓴다).
//
// Span에는 식별자와 분류만 기록한다. Runtime Contract가 trace에 남기지 않는 값(Terminal INPUT/OUTPUT, workspace file 경로·본문·목록,
// Secret, Token, Provider raw payload, 요청 URL의 query)은 어떤 key에도 담지 않는다. 식별자는 log의 correlation field와 같은 이름과 값이다.
const (
	AttrRequestID         = attribute.Key("labbit.request_id")
	AttrOperationID       = attribute.Key("labbit.operation_id")
	AttrLabInstanceID     = attribute.Key("labbit.lab_instance_id")
	AttrConnectorID       = attribute.Key("labbit.connector_id")
	AttrTerminalSessionID = attribute.Key("labbit.terminal_session_id")
	// AttrControlMessage는 Connector Control message type(TERMINAL_OPEN 등 contract에 정의된 고정 이름)이다.
	AttrControlMessage = attribute.Key("labbit.connector.message_type")
	// AttrResultTrace는 Connector가 돌려준 결과의 Trace Context를 우리가 보낸 command의 Context와 비교한 결과다.
	// same_trace, different_trace, absent 중 하나다.
	AttrResultTrace = attribute.Key("labbit.connector.result_trace")
	// AttrOutcome은 Connector가 보고한 결과(SUCCEEDED/FAILED)나 우리 쪽 분류(timeout 등)의 고정 값이다.
	AttrOutcome = attribute.Key("labbit.outcome")
	// AttrErrorCode는 log의 error_code와 같은 안전한 고정 분류다. 오류 문자열을 담지 않는다.
	AttrErrorCode = attribute.Key("labbit.error_code")
)
