# PostgreSQL Physical Schema SSOT

이 디렉터리의 명시적 SQL Migration 파일(`*.sql`)이 PostgreSQL Physical Schema의 SSOT입니다.

여기서 관리하는 대상:
- Table
- Column
- Primary Key
- Foreign Key
- Unique Constraint
- Index
- 기타 DB Constraint
- Schema Migration 이력

Application startup AutoMigration은 사용하지 않습니다.

초기 Physical Schema와 실제 Migration SQL은 별도 설계 작업에서 작성합니다.
