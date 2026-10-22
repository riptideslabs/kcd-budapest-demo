package main

import (
	"context"
	"encoding/json"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	sidecarName      = "egress-proxy"
	caVolume         = "egress-proxy-ca"
	socketVolume     = "egress-proxy-spire-socket"
	caDir            = "/run/egress-proxy"
	caFile           = caDir + "/ca.crt"
	socketDir        = "/run/spire/agent-sockets"
	injectedAnnotKey = "egress-proxy/injected"
)

// injector adds the egress proxy to pods created in namespaces labeled
// egress-proxy=enabled (the webhook's namespaceSelector does the matching).
type injector struct {
	decoder   admission.Decoder
	image     string
	configMap string // Anthropic IDs, in the pod's namespace
	proxyAddr string
}

func (i *injector) Handle(_ context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	if err := i.decoder.Decode(req, pod); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if pod.Annotations[injectedAnnotKey] == "true" || hasContainer(pod, sidecarName) {
		return admission.Allowed("already injected")
	}

	i.inject(pod)

	raw, err := json.Marshal(pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	logf("injected %s into a %s pod in namespace %s", sidecarName, podName(pod), req.Namespace)

	return admission.PatchResponseFromRaw(req.Object.Raw, raw)
}

func (i *injector) inject(pod *corev1.Pod) {
	always := corev1.ContainerRestartPolicyAlways
	probe := &corev1.Probe{
		ProbeHandler:  corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"egress-proxy", "healthcheck"}}},
		PeriodSeconds: 1,
	}

	// A native sidecar: it starts, and passes its startup probe (CA written,
	// listening), before the app containers start.
	sidecar := corev1.Container{
		Name:            sidecarName,
		Image:           i.image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		RestartPolicy:   &always,
		EnvFrom: []corev1.EnvFromSource{{
			ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: i.configMap}},
		}},
		Env: []corev1.EnvVar{
			{Name: "SPIFFE_ENDPOINT_SOCKET", Value: "unix://" + socketDir + "/spire-agent.sock"},
			{Name: "LISTEN_ADDR", Value: i.proxyAddr},
			{Name: "CA_CERT_OUT", Value: caFile},
		},
		StartupProbe:   withFailureThreshold(probe, 30),
		ReadinessProbe: probe,
		Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("16Mi"),
		}},
		SecurityContext: &corev1.SecurityContext{
			RunAsUser:                ptr(int64(65532)),
			RunAsNonRoot:             ptr(true),
			ReadOnlyRootFilesystem:   ptr(true),
			AllowPrivilegeEscalation: ptr(false),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: socketVolume, MountPath: socketDir, ReadOnly: true},
			{Name: caVolume, MountPath: caDir},
		},
	}
	pod.Spec.InitContainers = append([]corev1.Container{sidecar}, pod.Spec.InitContainers...)

	pod.Spec.Volumes = append(pod.Spec.Volumes,
		// Production would use the SPIFFE CSI driver instead of a hostPath.
		corev1.Volume{Name: socketVolume, VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: socketDir, Type: ptr(corev1.HostPathDirectory)},
		}},
		// The proxy's CA certificate. The CA key stays in the proxy's memory.
		corev1.Volume{Name: caVolume, VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory, SizeLimit: ptr(resource.MustParse("1Mi"))},
		}},
	)

	// Point every app container at the proxy and its CA, without overriding
	// anything the workload sets itself.
	env := []corev1.EnvVar{
		{Name: "HTTPS_PROXY", Value: "http://" + i.proxyAddr},
		{Name: "NO_PROXY", Value: "localhost,127.0.0.1,.svc,.cluster.local"},
		{Name: "CURL_CA_BUNDLE", Value: caFile},
		{Name: "SSL_CERT_FILE", Value: caFile},
		{Name: "REQUESTS_CA_BUNDLE", Value: caFile},
		{Name: "NODE_EXTRA_CA_CERTS", Value: caFile},
	}
	for c := range pod.Spec.Containers {
		ctr := &pod.Spec.Containers[c]
		for _, e := range env {
			if !hasEnv(ctr, e.Name) {
				ctr.Env = append(ctr.Env, e)
			}
		}
		ctr.VolumeMounts = append(ctr.VolumeMounts, corev1.VolumeMount{Name: caVolume, MountPath: caDir, ReadOnly: true})
	}

	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[injectedAnnotKey] = "true"
}

func hasContainer(pod *corev1.Pod, name string) bool {
	for _, c := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		if c.Name == name {
			return true
		}
	}

	return false
}

func hasEnv(c *corev1.Container, name string) bool {
	for _, e := range c.Env {
		if e.Name == name {
			return true
		}
	}

	return false
}

func podName(pod *corev1.Pod) string {
	if pod.Name != "" {
		return pod.Name
	}

	return pod.GenerateName + "…"
}

func withFailureThreshold(p *corev1.Probe, n int32) *corev1.Probe {
	c := *p
	c.FailureThreshold = n

	return &c
}

func ptr[T any](v T) *T { return &v }
