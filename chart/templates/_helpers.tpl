{{/*
Expand the name of the chart.
*/}}
{{- define "customrouter.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "customrouter.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "customrouter.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "customrouter.labels" -}}
helm.sh/chart: {{ include "customrouter.chart" . }}
{{ include "customrouter.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with .Values.global.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "customrouter.selectorLabels" -}}
app.kubernetes.io/name: {{ include "customrouter.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Operator name
*/}}
{{- define "customrouter.operator.name" -}}
{{- printf "%s-operator" (include "customrouter.fullname" .) }}
{{- end }}

{{/*
Operator labels
*/}}
{{- define "customrouter.operator.labels" -}}
{{ include "customrouter.labels" . }}
app.kubernetes.io/component: operator
control-plane: controller-manager
{{- end }}

{{/*
Operator selector labels
*/}}
{{- define "customrouter.operator.selectorLabels" -}}
{{ include "customrouter.selectorLabels" . }}
app.kubernetes.io/component: operator
control-plane: controller-manager
{{- end }}

{{/*
Operator service account name
*/}}
{{- define "customrouter.operator.serviceAccountName" -}}
{{- if .Values.operator.serviceAccount.create }}
{{- default (include "customrouter.operator.name" .) .Values.operator.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.operator.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
External processor name
*/}}
{{- define "customrouter.extproc.name" -}}
{{- $name := .name -}}
{{- $root := .root -}}
{{- if eq $name "default" }}
{{- printf "%s-extproc" (include "customrouter.fullname" $root) }}
{{- else }}
{{- printf "%s-extproc-%s" (include "customrouter.fullname" $root) $name }}
{{- end }}
{{- end }}

{{/*
External processor labels
*/}}
{{- define "customrouter.extproc.labels" -}}
{{- $root := .root -}}
{{- $name := .name -}}
{{ include "customrouter.labels" $root }}
app.kubernetes.io/component: external-processor
customrouter.freepik.com/extproc-name: {{ $name }}
{{- end }}

{{/*
External processor selector labels
*/}}
{{- define "customrouter.extproc.selectorLabels" -}}
{{- $root := .root -}}
{{- $name := .name -}}
{{ include "customrouter.selectorLabels" $root }}
app.kubernetes.io/component: external-processor
customrouter.freepik.com/extproc-name: {{ $name }}
{{- end }}

{{/*
External processor service account name
*/}}
{{- define "customrouter.extproc.serviceAccountName" -}}
{{- $name := .name -}}
{{- $config := .config -}}
{{- $root := .root -}}
{{- if $config.serviceAccount.create }}
{{- default (include "customrouter.extproc.name" (dict "name" $name "root" $root)) $config.serviceAccount.name }}
{{- else }}
{{- default "default" $config.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Image pull secrets
*/}}
{{- define "customrouter.imagePullSecrets" -}}
{{- with .Values.global.imagePullSecrets }}
imagePullSecrets:
{{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}
