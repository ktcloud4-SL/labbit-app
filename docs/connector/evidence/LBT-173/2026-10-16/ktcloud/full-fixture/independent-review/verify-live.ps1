param([Parameter(Mandatory=$true)][string]$PrivateDirectory)
$ErrorActionPreference='Stop'
$public=Split-Path $PSScriptRoot
$sha=[System.Security.Cryptography.SHA256]::Create()
$utf8=[System.Text.UTF8Encoding]::new($false)
$checks=[ordered]@{}
function ReadPrivate([string]$name){
 $file=Join-Path $PrivateDirectory $name
 if(-not (Test-Path -LiteralPath $file)){throw ('Required private artifact absent: '+$name)}
 Get-Content -LiteralPath $file -Raw | ConvertFrom-Json
}
function ReadPublic([string]$name){
 $file=Join-Path $public $name
 if(-not (Test-Path -LiteralPath $file)){throw ('Required public artifact absent: '+$name)}
 Get-Content -LiteralPath $file -Raw | ConvertFrom-Json
}
function HashText([string]$value){
 ([BitConverter]::ToString($sha.ComputeHash($utf8.GetBytes($value)))).Replace('-','').ToLowerInvariant()
}
function SameJson($a,$b){
 ($a|ConvertTo-Json -Depth 30 -Compress) -ceq ($b|ConvertTo-Json -Depth 30 -Compress)
}
function Check([string]$label,[bool]$condition){$checks[$label]=$condition}

$raw=ReadPrivate 'ktcloud-control-ledger.json'
$ledger=ReadPublic 'control-ledger.metadata.json'
foreach($key in @('stage','passed','hello','heartbeat','plannedGenerations')){
 Check ('ledger-'+$key) ($raw.$key -ceq $ledger.$key)
}
Check 'lab-id-hash' ((HashText $raw.labInstanceId) -ceq $ledger.labInstanceIdSha256)
Check 'resource-count-10' ($raw.resources.Count -eq 10 -and $ledger.resources.Count -eq 10)
$resourceMatch=$true
for($i=0;$i -lt $raw.resources.Count;$i++){
 $a=$raw.resources[$i];$b=$ledger.resources[$i]
 foreach($key in @('resourceType','generation','logicalName','observedState')){
  if($a.$key -cne $b.$key){$resourceMatch=$false}
 }
 if((HashText $a.providerId) -cne $b.providerIdSha256){$resourceMatch=$false}
}
Check 'typed-resource-metadata-exact' $resourceMatch
Check 'journal-true' ($raw.passed -eq $true -and $raw.hello -eq $true -and $raw.heartbeat -eq $true -and $raw.plannedGenerations -eq 2)
Check 'journal-final-graceful-stage' ($raw.stage -ceq 'PREVIEW_PRODUCTION_CONNECTOR_GRACEFUL_SHUTDOWN_JOINED_PASS')
Check 'all-lab-resources-deleted' (@($raw.resources|Where-Object {$_.observedState -cne 'DELETED'}).Count -eq 0)
Check 'generation-1-five-generation-2-five' (@($raw.resources|Where-Object {$_.generation -eq 1}).Count -eq 5 -and @($raw.resources|Where-Object {$_.generation -eq 2}).Count -eq 5)
Check 'unique-typed-ids-10' (@($raw.resources|ForEach-Object {$_.resourceType+':'+$_.providerId}|Sort-Object -Unique).Count -eq 10)
$types=@{}
foreach($item in $raw.resources){if(-not $types.ContainsKey($item.resourceType)){$types[$item.resourceType]=0};$types[$item.resourceType]++}
Check 'expected-lab-resource-types' ($types.Count -eq 4 -and $types['KT_TIER'] -eq 4 -and $types['KT_FIREWALL_POLICY'] -eq 2 -and $types['SERVER'] -eq 2 -and $types['VOLUME'] -eq 2)

$baseline=ReadPrivate 'baseline-inventory.json'
$final=ReadPrivate 'final-inventory.json'
$inventory=ReadPublic 'final-inventory.metadata.json'
$inventoryMatch=$true;$exactBaseline=$true
foreach($property in $baseline.PSObject.Properties){
 $kind=$property.Name
 $old=@($property.Value|Sort-Object -CaseSensitive)
 $now=@($final.inventory.$kind|Sort-Object -CaseSensitive)
 $entry=$inventory.kinds.$kind
 $equal=SameJson $old $now
 if(-not $equal){$exactBaseline=$false}
 $oldHash=HashText (ConvertTo-Json -InputObject $old -Compress)
 $nowHash=HashText (ConvertTo-Json -InputObject $now -Compress)
 if($old.Count -ne $entry.baselineCount -or $now.Count -ne $entry.finalCount -or $equal -ne $entry.exactIdsEqual -or $oldHash -cne $entry.baselineIdsSha256 -or $nowHash -cne $entry.finalIdsSha256){$inventoryMatch=$false}
}
Check 'seven-kind-inventory-metadata-exact' ($inventoryMatch -and @($baseline.PSObject.Properties).Count -eq 7 -and @($inventory.kinds.PSObject.Properties).Count -eq 7)
Check 'seven-kind-exact-baseline' ($exactBaseline -and $final.baselinePreserved -eq $true -and $final.deleted -eq $true -and $inventory.baselinePreserved -eq $true -and $inventory.bootstrapDeleted -eq $true)
Check 'different-kinds-empty' (@($final.differentKinds).Count -eq 0 -and @($inventory.differentKinds).Count -eq 0)
$bootstrapLedgers=@(Get-ChildItem -LiteralPath $PrivateDirectory -Filter '*ledger*.json' -File|ForEach-Object {Get-Content -LiteralPath $_.FullName -Raw|ConvertFrom-Json}|Where-Object {$null -ne $_.PSObject.Properties['keyCreated'] -and $_.serverId})
$bootstrapCount=0
if($bootstrapLedgers.Count -eq 1){
 $bootstrap=$bootstrapLedgers[0]
 $bootstrapCount=[int][bool]$bootstrap.serverId+@($bootstrap.volumeIds).Count+[int][bool]$bootstrap.keyCreated+@($bootstrap.policyIds).Count+[int][bool]$bootstrap.publicIpId+[int][bool]$bootstrap.forwardId
}
Check 'bootstrap-eight-owned-resources-cleaned' ($bootstrapLedgers.Count -eq 1 -and $bootstrapCount -eq 8 -and $bootstrap.deleted -eq $true -and $bootstrap.stage -ceq 'BOOTSTRAP_CLEANUP_CONFIRMED')
$bootstrapMetadata=ReadPublic 'bootstrap-ledger.metadata.json'
$expectedBootstrap=@(
 [pscustomobject]@{resourceType='SERVER';providerIdSha256=(HashText $bootstrap.serverId)}
 [pscustomobject]@{resourceType='KEYPAIR';providerIdSha256=(HashText $bootstrap.keypairName)}
 [pscustomobject]@{resourceType='PUBLIC_IP';providerIdSha256=(HashText $bootstrap.publicIpId)}
 [pscustomobject]@{resourceType='FORWARD';providerIdSha256=(HashText $bootstrap.forwardId)}
)
foreach($id in $bootstrap.volumeIds){$expectedBootstrap+=[pscustomobject]@{resourceType='VOLUME';providerIdSha256=(HashText $id)}}
foreach($id in $bootstrap.policyIds){$expectedBootstrap+=[pscustomobject]@{resourceType='POLICY';providerIdSha256=(HashText $id)}}
Check 'bootstrap-eight-resource-metadata-exact' ($bootstrapMetadata.deleted -eq $bootstrap.deleted -and $bootstrapMetadata.resourceCount -eq $bootstrapCount -and (SameJson $expectedBootstrap $bootstrapMetadata.resources))
$expectedBootstrapTypes=@{SERVER=1;KEYPAIR=1;PUBLIC_IP=1;FORWARD=1;VOLUME=1;POLICY=3}
$bootstrapTypesMatch=@($bootstrapMetadata.resourceTypeCounts.PSObject.Properties).Count -eq 6
foreach($key in $expectedBootstrapTypes.Keys){if($bootstrapMetadata.resourceTypeCounts.$key -ne $expectedBootstrapTypes[$key]){$bootstrapTypesMatch=$false}}
Check 'bootstrap-resource-type-counts-exact' $bootstrapTypesMatch

$residual=ReadPrivate 'ktcloud-residual-current.json'
$exportedResidual=ReadPublic 'final-residual.json'
Check 'residual-metadata-exact' (SameJson $residual $exportedResidual)
Check 'residual-ten-kinds-zero' ($residual.ownedResidualTotal -eq 0 -and @($residual.ownedResourceCounts.PSObject.Properties).Count -eq 10 -and @($residual.ownedResourceCounts.PSObject.Properties|Where-Object {$_.Value -ne 0}).Count -eq 0)
$residualKeys=@('at','ownedResourceCounts','ownedResidualTotal','basicTierCount','externalNetworkEntryCount','projectNetworkEntryCount','status')
Check 'residual-schema-safe' (@($residual.PSObject.Properties|Where-Object {$_.Name -notin $residualKeys}).Count -eq 0)

$phases=@(Get-Content -LiteralPath (Join-Path $PrivateDirectory 'phase-results.jsonl')|ForEach-Object {$_|ConvertFrom-Json})
$publishedPhases=ReadPublic 'phase-results.json'
Check 'phase-metadata-exact' (SameJson $phases $publishedPhases)
$expected=@('inventory','create','access','ssh','deploy','control','collect','residual','cleanup','residual','final')
Check 'eleven-ordered-phases-exit-zero' ((SameJson @($phases.phase) $expected) -and @($phases|Where-Object {$_.exitCode -ne 0}).Count -eq 0)
Check 'phase-log-relative-basename' (@($phases|Where-Object {[IO.Path]::IsPathRooted($_.log) -or $_.log -match '[/\\]'}).Count -eq 0)
Check 'phase-schema-safe' (@($phases|ForEach-Object {$_.PSObject.Properties.Name}|Where-Object {$_ -notin @('phase','started','finished','exitCode','log')}).Count -eq 0)

$prefix='^(Control evidence:|Production Provider query|Actual WSS |KT Preview |Safe Provider error:|Control acceptance failed:|Production Connector shutdown:)'
$lines=@(Get-Content -LiteralPath (Join-Path $PrivateDirectory 'ktcloud-control-acceptance.log')|Where-Object {$_ -cmatch $prefix})
$exportedLines=@(Get-Content -LiteralPath (Join-Path $public 'live-lifecycle.log'))
Check 'allowlisted-lifecycle-lines-exact' (SameJson $lines $exportedLines)
Check 'both-forwarding-generations-pass' (@($lines|Where-Object {$_ -cmatch '^KT Preview g[12]:.* guards PASS; bodies withheld$'}).Count -eq 2)
Check 'full-smoke-stage-recorded' ($lines -ccontains 'Control evidence: KT_PREVIEW_FORWARDING_SMOKE_PASS_OWNED_LAB_RESIDUAL_0')
Check 'graceful-child-exit-recorded' ($lines -ccontains 'Production Connector shutdown: exited successfully within 5s; forcedKill=false')
Check 'no-control-safe-error-lines' (@($lines|Where-Object {$_ -cmatch '^(Safe Provider error:|Control acceptance failed:)'}).Count -eq 0)

$deployment=@(Get-ChildItem -LiteralPath $PrivateDirectory -Filter 'deploy-*.log' -File|Sort-Object Name|ForEach-Object {Get-Content -LiteralPath $_.FullName}|Where-Object {$_ -cmatch '^(Verified deployed |Deployment:)'})
$exportedDeployment=@(Get-Content -LiteralPath (Join-Path $public 'deployment-digests.log'))
Check 'deployment-lines-exact' (SameJson $deployment $exportedDeployment)
$binaries=ReadPublic 'binary-manifest.json'
$deployedMatch=$true
foreach($name in @('labbit-connector-ktcloud','lbt145-control-linux','labbit-workspace-sftp')){
 $expectedLine='Verified deployed '+$name+' SHA256: '+$binaries.sha256.$name
 if($deployment -cnotcontains $expectedLine){$deployedMatch=$false}
}
Check 'three-deployed-digests-match-built-manifest' $deployedMatch
$binaryCopies=0;$binaryMatch=$true
foreach($property in $binaries.sha256.PSObject.Properties){
 $copies=@(Get-ChildItem -LiteralPath $PrivateDirectory -Recurse -File|Where-Object {$_.Name -ceq $property.Name})
 $binaryCopies+=$copies.Count
 if($copies.Count -eq 0){$binaryMatch=$false}
 foreach($file in $copies){if((Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant() -cne $property.Value){$binaryMatch=$false}}
}
Check 'all-prepared-binary-copies-match-manifest' $binaryMatch

$exported=@('control-ledger.metadata.json','bootstrap-ledger.metadata.json','final-inventory.metadata.json','final-residual.json','phase-results.json','live-lifecycle.log','deployment-digests.log')
$publishedText=($exported|ForEach-Object {Get-Content -LiteralPath (Join-Path $public $_) -Raw}) -join "`n"
$privateTokens=@($raw.labInstanceId)+@($raw.resources.providerId)+@($baseline.PSObject.Properties|ForEach-Object {$_.Value})+@($final.inventory.PSObject.Properties|ForEach-Object {$_.Value})
if($bootstrapLedgers.Count -eq 1){$privateTokens+=@($bootstrap.serverId,$bootstrap.keypairName,$bootstrap.publicIpId,$bootstrap.forwardId,$bootstrap.privateIP,$bootstrap.publicIP)+@($bootstrap.volumeIds)+@($bootstrap.policyIds)}
Check 'known-private-resource-ids-withheld' (@($privateTokens|Where-Object {$_ -and $publishedText.Contains([string]$_)}).Count -eq 0)
Check 'no-private-key-or-ipv4-in-metadata' (-not ($publishedText -match '-----BEGIN .*PRIVATE KEY-----|\b(?:\d{1,3}\.){3}\d{1,3}\b'))

$failed=@($checks.GetEnumerator()|Where-Object {-not $_.Value}|ForEach-Object {$_.Key})
$result=[ordered]@{checkedAtUTC=(Get-Date).ToUniversalTime().ToString('o');checks=$checks;failedChecks=$failed;rawResourceCount=$raw.resources.Count;resourceTypeCounts=$types;bootstrapOwnedResourceCount=$bootstrapCount;phaseCount=$phases.Count;lifecycleLines=$lines.Count;binaryCopies=$binaryCopies;cloudCallsByReviewer=0;scope='Read-only comparison of protected originals and safe exports. Prepared binary hashes plus deployed digest lines are checked separately. No cloud API or source edits.'}
$result|ConvertTo-Json -Depth 8|Set-Content -LiteralPath (Join-Path $PSScriptRoot 'final-live-check.json') -Encoding utf8
$result|ConvertTo-Json -Depth 8
if($failed.Count -ne 0){exit 1}
