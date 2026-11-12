{{- define "client-webhook-cert-manager.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "client-webhook-cert-manager.fullname" -}}
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

{{- define "client-webhook-cert-manager.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "client-webhook-cert-manager.labels" -}}
helm.sh/chart: {{ include "client-webhook-cert-manager.chart" . }}
{{ include "client-webhook-cert-manager.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "client-webhook-cert-manager.selectorLabels" -}}
app.kubernetes.io/name: {{ include "client-webhook-cert-manager.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "client-webhook-cert-manager.serviceAccountName" -}}
{{- include "client-webhook-cert-manager.fullname" . -}}
{{- end -}}

{{- define "client-webhook-cert-manager.caIssuerName" -}}
{{- printf "%s-ca" (include "client-webhook-cert-manager.fullname" . | trunc 60 | trimSuffix "-") -}}
{{- end -}}

{{- define "client-webhook-cert-manager.selfSignedIssuerName" -}}
{{- printf "%s-selfsigned" (include "client-webhook-cert-manager.fullname" . | trunc 52 | trimSuffix "-") -}}
{{- end -}}

{{- define "client-webhook-cert-manager.caCertificateName" -}}
{{- include "client-webhook-cert-manager.caIssuerName" . -}}
{{- end -}}

{{- define "client-webhook-cert-manager.caSecretName" -}}
{{- include "client-webhook-cert-manager.caIssuerName" . -}}
{{- end -}}

{{- define "client-webhook-cert-manager.authDelegatorName" -}}
{{- printf "%s-auth-delegator" (include "client-webhook-cert-manager.fullname" . | trunc 44 | trimSuffix "-") | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "client-webhook-cert-manager.domainSolverName" -}}
{{- printf "%s-domain-solver" (include "client-webhook-cert-manager.fullname" . | trunc 47 | trimSuffix "-") | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "client-webhook-cert-manager.authenticationReaderName" -}}
{{- printf "%s-webhook-authentication-reader" (include "client-webhook-cert-manager.fullname" . | trunc 32 | trimSuffix "-") | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "client-webhook-cert-manager.servingSecretName" -}}
{{- printf "%s-serving-cert" (include "client-webhook-cert-manager.fullname" . | trunc 50 | trimSuffix "-") -}}
{{- end -}}

{{- define "client-webhook-cert-manager.servingCertificateName" -}}
{{- include "client-webhook-cert-manager.servingSecretName" . -}}
{{- end -}}
