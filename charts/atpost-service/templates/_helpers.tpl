{{/*
Expand the service name. Use .Values.service.name if set; otherwise
fall back to the release name so a forgotten override at least
produces something deterministic.
*/}}
{{- define "atpost-service.name" -}}
{{- default .Release.Name .Values.service.name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Standard labels — applied to every object the chart emits. The
app.kubernetes.io/* labels follow the kubernetes convention; the
extras let ArgoCD + observability tooling find related resources.
*/}}
{{- define "atpost-service.labels" -}}
app.kubernetes.io/name: {{ include "atpost-service.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: atpost
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end }}

{{/*
Selector labels — narrower set used in Deployment.spec.selector.
Mutating the labels here breaks rolling updates (selector is immutable
on Deployments), so additions should go in the "labels" block only.
*/}}
{{- define "atpost-service.selectorLabels" -}}
app.kubernetes.io/name: {{ include "atpost-service.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name. By default tracks the release name; override via
serviceAccount.name if you need to share an SA across releases.
*/}}
{{- define "atpost-service.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "atpost-service.name" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Validation: fail the render if any REQUIRED field is missing. Better
to fail at `helm template` time than to deploy a half-broken manifest.
*/}}
{{- define "atpost-service.validate" -}}
{{- if not .Values.service.name }}
{{- fail "service.name is required" }}
{{- end }}
{{- if not .Values.service.port }}
{{- fail "service.port is required" }}
{{- end }}
{{- if not .Values.image.repository }}
{{- fail "image.repository is required" }}
{{- end }}
{{- if not .Values.image.tag }}
{{- fail "image.tag is required (set by CI; do not hand-edit)" }}
{{- end }}
{{- if and .Values.externalSecret.enabled (not .Values.externalSecret.remoteKey) }}
{{- fail "externalSecret.remoteKey is required when externalSecret.enabled" }}
{{- end }}
{{- end }}
