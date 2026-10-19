# Historical runs

`local-openstack-initial-failure.log` is the first actual Preview transport attempt. It passed initial HTTP/session/PTY/SFTP/error steps and failed waiting for the stale-generation OPEN result after Reset. It completed tracked Lab cleanup/absence checks (21 duplicate-inclusive entries). Exact uncommitted source hashes were not captured for that run; do not use it as final-source Evidence.

`source96ec/` preserves the later source `96ec0a79b149ac980d55dd8434a639f38ffa4d5d7f4dd8b480c5d3304db87e44`, before direct-replacement idle retirement and latest-main refresh. That source passed actual local fixture, full serial Go, Connector9-package race and web checks. Its first full parallel Go run and 20-repeat timing run failed; those original logs are retained alongside their metadata. The final source is `956c7d6f3b87f6d49d1b842571124da4591db40f5bc15de5c6efce9d65fe98b9`; use the parent folder's final results for that source.

The first reviewer snapshots and second-pass open R008 verdict are historical intermediate records. The final review supersedes their unresolved status, without changing their original observations.
