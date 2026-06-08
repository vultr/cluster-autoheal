{{- define "cluster-autoheal.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cluster-autoheal.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "cluster-autoheal.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cluster-autoheal.labels" -}}
helm.sh/chart: {{ include "cluster-autoheal.chart" . }}
{{ include "cluster-autoheal.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "cluster-autoheal.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cluster-autoheal.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "cluster-autoheal.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "cluster-autoheal.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "cluster-autoheal.vultrSecretName" -}}
{{- default (printf "%s-vultr" (include "cluster-autoheal.fullname" .)) .Values.vultr.existingSecret -}}
{{- end -}}
