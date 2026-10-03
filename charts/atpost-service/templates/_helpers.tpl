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
{{- /*
  The AWS account id is passed at deploy time (ArgoCD helm parameter or
  --set-string) and used by every values file in tpl expressions. Validate it
  HERE, once, so a values file that forgot `required` still cannot render an
  image repository or IRSA ARN with an empty or fake account.
*/}}
{{- $acct := toString (default "" .Values.global.awsAccountId) }}
{{- if not (regexMatch "^[0-9]{12}$" $acct) }}
{{- fail (printf "global.awsAccountId is required and must be the 12-digit AWS account id (got %q); pass it with --set-string global.awsAccountId=<id> or the ApplicationSet parameter" $acct) }}
{{- end }}
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
{{- if and .Values.migrationJob.enabled (not .Values.migrationJob.command) }}
{{- fail "migrationJob.command is required when migrationJob.enabled" }}
{{- end }}
{{- if .Values.webhookIngress.enabled }}
{{- if not .Values.webhookIngress.host }}{{ fail "webhookIngress.host is required when webhookIngress.enabled" }}{{ end }}
{{- if not .Values.webhookIngress.path }}{{ fail "webhookIngress.path is required when webhookIngress.enabled" }}{{ end }}
{{- end }}
{{- if .Values.networkPolicy.enabled }}
{{- if not .Values.networkPolicy.ingressFrom }}{{ fail "networkPolicy.ingressFrom must list at least one peer when networkPolicy.enabled (an empty `from` would allow everything)" }}{{ end }}
{{- range .Values.networkPolicy.ingressFrom }}
{{- if and .ingressController (not $.Values.networkPolicy.ingressControllerCidrs) }}{{ fail "networkPolicy.ingressControllerCidrs is required when a peer sets ingressController: true (the VPC CIDR, infra/terraform/envs/prod/main.tf module vpc)" }}{{ end }}
{{- end }}
{{- end }}
{{- if .Values.worker.enabled }}
{{- if not .Values.worker.image.repository }}{{ fail "worker.image.repository is required when worker.enabled" }}{{ end }}
{{- if not .Values.worker.image.tag }}{{ fail "worker.image.tag is required when worker.enabled (set by CI; never latest)" }}{{ end }}
{{- end }}
{{- /*
  Unfilled Terraform placeholders: a values file carries `__TF_<OUTPUT>__`
  where a Terraform output belongs (deploy/README.md lists them and
  deploy/fill-tf-outputs.sh fills them). Rendering one would ship the literal
  string as a bucket name or ARN, which fails at runtime in a way that looks
  like an AWS permissions problem. Refuse here instead, naming the token.
*/}}
{{- if not .Values.global.allowPlaceholders }}
{{- $found := regexFindAll "__TF_[A-Z0-9_]+__" (toYaml .Values) -1 | uniq }}
{{- if $found }}
{{- fail (printf "unfilled Terraform placeholder(s) %s in the values for %s: replace each with the matching `terraform output` (deploy/README.md, deploy/fill-tf-outputs.sh)" (join ", " $found) .Values.service.name) }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Environment block shared by the server container, the schema-dependency init
container, the worker and the migration Job. Order matters: commonEnv (per
environment) first, then the per-service env, so a service can override a
convention. Kubernetes keeps the LAST entry of a duplicated name.
Usage: {{- include "atpost-service.commonAndServiceEnv" . | nindent N }}
*/}}
{{- define "atpost-service.commonAndServiceEnv" -}}
{{- range $k, $v := .Values.commonEnv }}
- name: {{ $k }}
  value: {{ $v | quote }}
{{- end }}
{{- range $k, $v := .Values.env }}
- name: {{ $k }}
  value: {{ $v | quote }}
{{- end }}
{{- end }}

{{/*
envFrom entries shared the same way: envFrom and extraEnvFrom are both
verbatim Kubernetes envFrom entries ({secretRef: {name: x}} /
{configMapRef: {name: x}}; the server container always rendered envFrom this
way — the init container's older {kind,name} form was never used by any
values file and is retired here), then the ExternalSecret-minted Secret
(named after the release) when enabled.
*/}}
{{- define "atpost-service.envFrom" -}}
{{- range .Values.envFrom }}
- {{- toYaml . | nindent 2 }}
{{- end }}
{{- range .Values.extraEnvFrom }}
- {{- toYaml . | nindent 2 }}
{{- end }}
{{- if .Values.externalSecret.enabled }}
- secretRef:
    name: {{ include "atpost-service.name" . }}
{{- end }}
{{- end }}

{{/*
The server image reference. The repository is a tpl expression (it carries the
account id); the tag is stamped by CI.
*/}}
{{- define "atpost-service.image" -}}
{{ tpl .Values.image.repository . }}:{{ .Values.image.tag }}
{{- end }}
