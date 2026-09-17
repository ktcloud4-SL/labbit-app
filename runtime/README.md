# Runtime Contract

이 디렉터리는 Labbit 애플리케이션이 플랫폼에 요구하는 실행 조건의 출발점입니다.

## Application side

향후 이 저장소에서 정의할 항목:
- executable
- listening port
- environment variables
- secret / non-secret configuration
- health / readiness behavior
- graceful shutdown behavior
- logging requirements

## Platform side

실제 Kubernetes/AWS 배포 설정은 별도 `labbit-platform` 저장소의 Helm/IaC/GitOps에서 관리합니다.

예:
- Kubernetes Deployment / Service
- Probe configuration
- Secret injection
- Replica / HPA
- Ingress / Gateway
- termination grace / connection draining
- environment-specific deployment configuration

구체적인 값은 구현·배포 검증 전에 이 문서에서 임의로 고정하지 않습니다.
