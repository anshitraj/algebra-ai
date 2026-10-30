#!/bin/bash
# Creates the alerts for the Algebra project: an email channel, and policies for CPU, memory and disk
# on the VM (the last two come from the Ops Agent). Run once (it adds, it does not look for duplicates).
# The uptime check on https://api.usealgebra.xyz/readyz is created after that hostname exists:
#   deploy/gcp/monitoring.sh uptime
#
# Usage: ALERT_EMAIL=you@example.com deploy/gcp/monitoring.sh [uptime]
set -euo pipefail
PROJECT=${PROJECT:-algebra-prod-7802b5}
API=https://monitoring.googleapis.com/v3/projects/$PROJECT
TOKEN=$(gcloud auth print-access-token)
post() { curl -fsS -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "$2" "$API/$1"; }
# The resource name from a JSON answer (node is on every machine that builds the web app).
name() { node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>console.log(JSON.parse(s).name))'; }

if [ "${1:-}" = "uptime" ]; then
  : "${HOST:=api.usealgebra.xyz}"
  post uptimeCheckConfigs "$(cat <<EOF
{"displayName":"Algebra API readiness ($HOST)","period":"60s","timeout":"10s",
 "monitoredResource":{"type":"uptime_url","labels":{"project_id":"$PROJECT","host":"$HOST"}},
 "httpCheck":{"path":"/readyz","port":443,"useSsl":true,"validateSsl":true},
 "contentMatchers":[{"content":"\"postgres\":\"ok\"","matcher":"CONTAINS_STRING"}]}
EOF
)" | name
  echo "Uptime check created. Add an alert policy on it in the console (Monitoring > Alerting) or re-run with the channel."
  exit 0
fi

: "${ALERT_EMAIL:?set ALERT_EMAIL (where alerts are sent)}"
CHANNEL=$(post notificationChannels "{\"type\":\"email\",\"displayName\":\"Algebra alerts\",\"labels\":{\"email_address\":\"$ALERT_EMAIL\"}}" | name)
echo "channel: $CHANNEL"

policy() { # name, metric filter, threshold, duration
  post alertPolicies "$(cat <<EOF
{"displayName":"$1","combiner":"OR","enabled":true,
 "conditions":[{"displayName":"$1","conditionThreshold":{
   "filter":"$2","comparison":"COMPARISON_GT","thresholdValue":$3,"duration":"$4",
   "aggregations":[{"alignmentPeriod":"60s","perSeriesAligner":"ALIGN_MEAN"}]}}],
 "notificationChannels":["$CHANNEL"],"alertStrategy":{"autoClose":"1800s"}}
EOF
)" | name
}
policy "Algebra VM: CPU above 80% for 10 minutes" \
  'resource.type=\"gce_instance\" AND metric.type=\"compute.googleapis.com/instance/cpu/utilization\"' 0.8 600s
policy "Algebra VM: memory above 85% for 10 minutes" \
  'resource.type=\"gce_instance\" AND metric.type=\"agent.googleapis.com/memory/percent_used\" AND metric.labels.state=\"used\"' 85 600s
policy "Algebra VM: disk above 80%" \
  'resource.type=\"gce_instance\" AND metric.type=\"agent.googleapis.com/disk/percent_used\" AND metric.labels.state=\"used\"' 80 300s
