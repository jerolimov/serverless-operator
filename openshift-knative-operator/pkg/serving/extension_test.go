package serving

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift-knative/serverless-operator/openshift-knative-operator/pkg/common"
	"github.com/openshift-knative/serverless-operator/openshift-knative-operator/pkg/monitoring"
	ocpclient "github.com/openshift-knative/serverless-operator/pkg/client/injection/client"
	ocpfake "github.com/openshift-knative/serverless-operator/pkg/client/injection/client/fake"
	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"knative.dev/operator/pkg/apis/operator/v1alpha1"
	operator "knative.dev/operator/pkg/reconciler/common"
	"knative.dev/pkg/apis"
	kubeclient "knative.dev/pkg/client/injection/kube/client"
	kubefake "knative.dev/pkg/client/injection/kube/client/fake"
)

var (
	defaultIngress = &configv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.IngressSpec{
			Domain: "routing.example.com",
		},
	}

	servingNamespace = corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "knative-serving",
		},
	}
)

const defaultK8sVersion = "v1.20.0"

func init() {
	os.Setenv("IMAGE_foo", "bar")
	os.Setenv("IMAGE_default", "bar2")
	os.Setenv("IMAGE_queue-proxy", "baz")
	os.Setenv(requiredNsEnvName, servingNamespace.Name)
}

func TestReconcile(t *testing.T) {
	defaultIngress := &configv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.IngressSpec{
			Domain: "routing.example.com",
		},
	}

	cases := []struct {
		name       string
		k8sVersion string
		in         *v1alpha1.KnativeServing
		objs       []runtime.Object
		expected   *v1alpha1.KnativeServing
	}{{
		name:     "all nil",
		in:       &v1alpha1.KnativeServing{},
		expected: ks(),
	}, {
		name: "different HA settings",
		in: &v1alpha1.KnativeServing{
			Spec: v1alpha1.KnativeServingSpec{
				CommonSpec: v1alpha1.CommonSpec{
					HighAvailability: &v1alpha1.HighAvailability{
						Replicas: 3,
					},
				},
			},
		},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			ks.Spec.HighAvailability.Replicas = 3
		}),
	}, {
		name: "different certificate settings",
		in: &v1alpha1.KnativeServing{
			Spec: v1alpha1.KnativeServingSpec{
				ControllerCustomCerts: v1alpha1.CustomCerts{
					Type: "Secret",
					Name: "foo",
				},
			},
		},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			ks.Spec.ControllerCustomCerts.Type = "Secret"
			ks.Spec.ControllerCustomCerts.Name = "foo"
		}),
	}, {
		name: "existing logging route",
		in:   &v1alpha1.KnativeServing{},
		objs: []runtime.Object{
			defaultIngress,
			&routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "openshift-logging",
					Name:      "kibana",
				},
				Status: routev1.RouteStatus{
					Ingress: []routev1.RouteIngress{{
						Host: "logging.example.com",
					}},
				},
			},
		},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			common.Configure(&ks.Spec.CommonSpec, monitoring.ObservabilityCMName, "logging.revision-url-template",
				fmt.Sprintf(loggingURLTemplate, "logging.example.com"))
		}),
	}, {
		name: "override image settings",
		in: &v1alpha1.KnativeServing{
			Spec: v1alpha1.KnativeServingSpec{
				CommonSpec: v1alpha1.CommonSpec{
					Registry: v1alpha1.Registry{
						Override: map[string]string{
							"foo":         "not",
							"queue-proxy": "correct",
						},
					},
				},
			},
		},
		expected: ks(),
	}, {
		name: "override ingress class",
		in: &v1alpha1.KnativeServing{
			Spec: v1alpha1.KnativeServingSpec{
				CommonSpec: v1alpha1.CommonSpec{
					Config: v1alpha1.ConfigMapData{
						"network": map[string]string{
							"ingress.class": "foo",
						},
					},
				},
			},
		},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			common.Configure(&ks.Spec.CommonSpec, "network", "ingress.class", "foo")
		}),
	}, {
		name: "default kourier service type",
		in: &v1alpha1.KnativeServing{
			Spec: v1alpha1.KnativeServingSpec{
				Ingress: &v1alpha1.IngressConfigs{
					Kourier: v1alpha1.KourierIngressConfiguration{
						Enabled: true,
					},
				},
			},
		},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			ks.Spec.Ingress = &v1alpha1.IngressConfigs{
				Kourier: v1alpha1.KourierIngressConfiguration{
					Enabled:     true,
					ServiceType: "ClusterIP",
				},
			}
		}),
	}, {
		name: "override kourier service type",
		in: &v1alpha1.KnativeServing{
			Spec: v1alpha1.KnativeServingSpec{
				Ingress: &v1alpha1.IngressConfigs{
					Kourier: v1alpha1.KourierIngressConfiguration{
						Enabled:     true,
						ServiceType: "LoadBalancer",
					},
				},
			},
		},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			ks.Spec.Ingress = &v1alpha1.IngressConfigs{
				Kourier: v1alpha1.KourierIngressConfiguration{
					Enabled:     true,
					ServiceType: "LoadBalancer",
				},
			}
		}),
	}, {
		name: "override ingress config",
		in: &v1alpha1.KnativeServing{
			Spec: v1alpha1.KnativeServingSpec{
				Ingress: &v1alpha1.IngressConfigs{
					Istio: v1alpha1.IstioIngressConfiguration{
						Enabled: true,
					},
				},
			},
		},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			ks.Spec.Ingress = &v1alpha1.IngressConfigs{
				Istio: v1alpha1.IstioIngressConfiguration{
					Enabled: true,
				},
			}
			common.Configure(&ks.Spec.CommonSpec, "network", "ingress.class", istioIngressClassName)
			common.Configure(&ks.Spec.CommonSpec, monitoring.ObservabilityCMName, monitoring.ObservabilityBackendKey, "none")
		}),
	}, {
		name: "fix 'wrong' ingress config", // https://github.com/knative/operator/issues/568
		in: &v1alpha1.KnativeServing{
			Spec: v1alpha1.KnativeServingSpec{
				Ingress: &v1alpha1.IngressConfigs{
					Istio: v1alpha1.IstioIngressConfiguration{
						Enabled: false,
					},
					Kourier: v1alpha1.KourierIngressConfiguration{
						Enabled: false,
					},
					Contour: v1alpha1.ContourIngressConfiguration{
						Enabled: false,
					},
				},
			},
		},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			ks.Spec.Ingress = &v1alpha1.IngressConfigs{
				Kourier: v1alpha1.KourierIngressConfiguration{
					Enabled:     true,
					ServiceType: "ClusterIP",
				},
			}
		}),
	}, {
		name: "respect kourier settings",
		in: &v1alpha1.KnativeServing{
			Spec: v1alpha1.KnativeServingSpec{
				Ingress: &v1alpha1.IngressConfigs{
					Kourier: v1alpha1.KourierIngressConfiguration{
						// Enabled: true omitted explicitly.
						ServiceType: corev1.ServiceTypeClusterIP,
					},
				},
			},
		},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			ks.Spec.Ingress = &v1alpha1.IngressConfigs{
				Kourier: v1alpha1.KourierIngressConfiguration{
					Enabled:     true,
					ServiceType: corev1.ServiceTypeClusterIP,
				},
			}
		}),
	}, {
		name: "override default url scheme",
		in: &v1alpha1.KnativeServing{
			Spec: v1alpha1.KnativeServingSpec{
				CommonSpec: v1alpha1.CommonSpec{
					Config: v1alpha1.ConfigMapData{
						"network": map[string]string{
							"defaultExternalScheme": "http",
						},
					},
				},
			},
		},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			common.Configure(&ks.Spec.CommonSpec, "network", "defaultExternalScheme", "http")
		}),
	}, {
		name: "override autocreateClusterDomainClaims config",
		in: &v1alpha1.KnativeServing{
			Spec: v1alpha1.KnativeServingSpec{
				CommonSpec: v1alpha1.CommonSpec{
					Config: v1alpha1.ConfigMapData{
						"network": map[string]string{
							"autocreateClusterDomainClaims": "false",
						},
					},
				},
			},
		},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			common.Configure(&ks.Spec.CommonSpec, "network", "autocreateClusterDomainClaims", "false")
		}),
	}, {
		name: "respects different status",
		in: ks(func(ks *v1alpha1.KnativeServing) {
			ks.Status.MarkDependenciesInstalled()
		}),
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			ks.Status.MarkDependenciesInstalled()
		}),
	}, {
		name: "wrong namespace",
		in: ks(func(ks *v1alpha1.KnativeServing) {
			ks.Namespace = "foo"
		}),
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			ks.Namespace = "foo"
			ks.Status.MarkInstallFailed(`Knative Serving must be installed into the namespace "knative-serving"`)
		}),
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Default the namespace to the correct one if not set for brevity.
			if c.in.Namespace == "" {
				c.in.Namespace = servingNamespace.Name
			}

			objs := c.objs
			if objs == nil {
				objs = []runtime.Object{defaultIngress}
			}
			ks := c.in.DeepCopy()
			ctx, _ := ocpfake.With(context.Background(), objs...)
			ctx, _ = kubefake.With(ctx, &servingNamespace)
			ext := newFakeExtension(ctx, t)
			ext.Reconcile(context.Background(), ks)
			// Ignore time differences.
			opt := cmp.Comparer(func(apis.VolatileTime, apis.VolatileTime) bool {
				return true
			})
			if !cmp.Equal(ks, c.expected, opt) {
				t.Errorf("Got = %v, want: %v, diff:\n%s", ks, c.expected, cmp.Diff(ks, c.expected, opt))
			}
		})
	}
}

func newFakeExtension(ctx context.Context, t *testing.T) operator.Extension {
	kclient := kubeclient.Get(ctx)
	fakeDiscovery, ok := kclient.Discovery().(*fakediscovery.FakeDiscovery)
	if !ok {
		t.Fatalf("couldn't convert Discovery() to *FakeDiscovery")
	}

	fakeDiscovery.FakedServerVersion = &version.Info{
		GitVersion: defaultK8sVersion,
	}

	return &extension{
		ocpclient:  ocpclient.Get(ctx),
		kubeclient: kclient,
	}
}

func TestMonitoring(t *testing.T) {
	cases := []struct {
		name     string
		in       *v1alpha1.KnativeServing
		expected *v1alpha1.KnativeServing
		// Returns the expected status for monitoring
		setupMonitoringToggle func() (bool, error)
	}{{
		name:                  "enable monitoring when monitoring toggle is not defined, backend is not defined",
		in:                    &v1alpha1.KnativeServing{},
		expected:              ks(),
		setupMonitoringToggle: func() (bool, error) { return true, nil },
	}, {
		name: "enable monitoring when monitoring toggle = not defined, backend = defined and not `none`",
		in: &v1alpha1.KnativeServing{
			Spec: v1alpha1.KnativeServingSpec{
				CommonSpec: v1alpha1.CommonSpec{
					Config: map[string]map[string]string{monitoring.ObservabilityCMName: {monitoring.ObservabilityBackendKey: "prometheus"}},
				},
			},
		},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			common.Configure(&ks.Spec.CommonSpec, monitoring.ObservabilityCMName, monitoring.ObservabilityBackendKey, "prometheus")
		}),
		setupMonitoringToggle: func() (bool, error) { return true, nil },
	}, {
		name: "disable monitoring when monitoring toggle is not defined, backend is `none`",
		in: &v1alpha1.KnativeServing{
			Spec: v1alpha1.KnativeServingSpec{
				CommonSpec: v1alpha1.CommonSpec{
					Config: map[string]map[string]string{monitoring.ObservabilityCMName: {monitoring.ObservabilityBackendKey: "none"}},
				},
			},
		},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			common.Configure(&ks.Spec.CommonSpec, monitoring.ObservabilityCMName, monitoring.ObservabilityBackendKey, "none")
		}),
		setupMonitoringToggle: func() (bool, error) { return false, nil },
	}, {
		name:                  "enable monitoring when monitoring toggle is on, backend is not defined",
		in:                    &v1alpha1.KnativeServing{},
		expected:              ks(),
		setupMonitoringToggle: func() (bool, error) { return true, os.Setenv(monitoring.EnableMonitoringEnvVar, "true") },
	}, {
		name: "enable monitoring when monitoring toggle is on, backend is defined and not `none`",
		in: &v1alpha1.KnativeServing{
			Spec: v1alpha1.KnativeServingSpec{
				CommonSpec: v1alpha1.CommonSpec{
					Config: map[string]map[string]string{monitoring.ObservabilityCMName: {monitoring.ObservabilityBackendKey: "prometheus"}},
				},
			},
		},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			common.Configure(&ks.Spec.CommonSpec, monitoring.ObservabilityCMName, monitoring.ObservabilityBackendKey, "prometheus")
		}),
		setupMonitoringToggle: func() (bool, error) {
			return true, os.Setenv(monitoring.EnableMonitoringEnvVar, "true")
		},
	}, {
		name: "disable monitoring when monitoring toggle is on, backend is `none`",
		in: &v1alpha1.KnativeServing{
			Spec: v1alpha1.KnativeServingSpec{
				CommonSpec: v1alpha1.CommonSpec{
					Config: map[string]map[string]string{monitoring.ObservabilityCMName: {monitoring.ObservabilityBackendKey: "none"}},
				},
			},
		},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			common.Configure(&ks.Spec.CommonSpec, monitoring.ObservabilityCMName, monitoring.ObservabilityBackendKey, "none")
		}),
		setupMonitoringToggle: func() (bool, error) {
			return false, os.Setenv(monitoring.EnableMonitoringEnvVar, "true")
		},
	}, {
		name: "disable monitoring when monitoring toggle is off, backend is not defined",
		in:   &v1alpha1.KnativeServing{},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			common.Configure(&ks.Spec.CommonSpec, monitoring.ObservabilityCMName, monitoring.ObservabilityBackendKey, "none")
		}),
		setupMonitoringToggle: func() (bool, error) { return false, os.Setenv(monitoring.EnableMonitoringEnvVar, "false") },
	}, {
		name: "enable monitoring when monitoring toggle = off, backend = defined and not `none`",
		in: &v1alpha1.KnativeServing{
			Spec: v1alpha1.KnativeServingSpec{
				CommonSpec: v1alpha1.CommonSpec{
					Config: map[string]map[string]string{monitoring.ObservabilityCMName: {monitoring.ObservabilityBackendKey: "prometheus"}},
				},
			},
		},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			common.Configure(&ks.Spec.CommonSpec, monitoring.ObservabilityCMName, monitoring.ObservabilityBackendKey, "prometheus")
		}),
		setupMonitoringToggle: func() (bool, error) { return true, os.Setenv(monitoring.EnableMonitoringEnvVar, "false") },
	}, {
		name: "disable monitoring when monitoring toggle is off, backend is `none`",
		in: &v1alpha1.KnativeServing{
			Spec: v1alpha1.KnativeServingSpec{
				CommonSpec: v1alpha1.CommonSpec{
					Config: map[string]map[string]string{monitoring.ObservabilityCMName: {monitoring.ObservabilityBackendKey: "none"}},
				},
			},
		},
		expected: ks(func(ks *v1alpha1.KnativeServing) {
			common.Configure(&ks.Spec.CommonSpec, monitoring.ObservabilityCMName, monitoring.ObservabilityBackendKey, "none")
		}),
		setupMonitoringToggle: func() (bool, error) { return false, os.Setenv(monitoring.EnableMonitoringEnvVar, "false") },
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			objs := []runtime.Object{defaultIngress, &servingNamespace}
			ks := c.in.DeepCopy()
			ks.Namespace = servingNamespace.Name
			c.expected.Namespace = ks.Namespace
			ctx, _ := ocpfake.With(context.Background(), objs...)
			ctx, kube := kubefake.With(ctx, &servingNamespace)
			ext := newFakeExtension(ctx, t)
			shouldEnableMonitoring, err := c.setupMonitoringToggle()

			if err != nil {
				t.Errorf("Failed to setup the monitoring toggle %w", err)
			}
			ext.Reconcile(context.Background(), ks)

			// Ignore time differences.
			opt := cmp.Comparer(func(apis.VolatileTime, apis.VolatileTime) bool {
				return true
			})
			if !cmp.Equal(ks, c.expected, opt) {
				t.Errorf("Got = %v, want: %v, diff:\n%s", ks, c.expected, cmp.Diff(ks, c.expected, opt))
			}
			ns, err := kube.CoreV1().Namespaces().Get(context.Background(), ks.Namespace, metav1.GetOptions{})
			if err != nil {
				t.Errorf("Failed to get namespace %s: %v", ns, err)
			}
			if ns.Labels[monitoring.EnableMonitoringLabel] != strconv.FormatBool(shouldEnableMonitoring) {
				t.Errorf("Label is missing for namespace %s ", ks.Namespace)
			}
		})
	}
}

func ks(mods ...func(*v1alpha1.KnativeServing)) *v1alpha1.KnativeServing {
	base := &v1alpha1.KnativeServing{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: servingNamespace.Name,
		},
		Spec: v1alpha1.KnativeServingSpec{
			CommonSpec: v1alpha1.CommonSpec{
				HighAvailability: &v1alpha1.HighAvailability{
					Replicas: 2,
				},
				Config: v1alpha1.ConfigMapData{
					"deployment": map[string]string{
						"queueSidecarImage": "baz",
					},
					"domain": map[string]string{
						"routing.example.com": "",
					},
					"network": map[string]string{
						"domainTemplate":                defaultDomainTemplate,
						"ingress.class":                 kourierIngressClassName,
						"autocreateClusterDomainClaims": "true",
						"defaultExternalScheme":         "https",
					},
				},
				Registry: v1alpha1.Registry{
					Default: "bar2",
					Override: map[string]string{
						"default":     "bar2",
						"foo":         "bar",
						"queue-proxy": "baz",
					},
				},
				Resources: []v1alpha1.ResourceRequirementsOverride{{
					Container: "webhook",
					ResourceRequirements: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("1024Mi"),
						},
					},
				}},
			},
			ControllerCustomCerts: v1alpha1.CustomCerts{
				Type: "ConfigMap",
				Name: "config-service-ca",
			},
			Ingress: &v1alpha1.IngressConfigs{
				Kourier: v1alpha1.KourierIngressConfiguration{
					Enabled:     true,
					ServiceType: "ClusterIP",
				},
			},
		},
	}

	for _, mod := range mods {
		mod(base)
	}

	return base
}

type testVersioner struct {
	version string
	err     error
}

func (t *testVersioner) ServerVersion() (*version.Info, error) {
	return &version.Info{GitVersion: t.version}, t.err
}

func TestVersionCheck(t *testing.T) {
	tests := []struct {
		name          string
		actualVersion *testVersioner
		wantError     bool
	}{{
		name:          "greater version (patch)",
		actualVersion: &testVersioner{version: "v1.20.0"},
	}, {
		name:          "greater version (patch), no v",
		actualVersion: &testVersioner{version: "1.20.0"},
	}, {
		name:          "greater version (patch), pre-release",
		actualVersion: &testVersioner{version: "1.20.2-kpn-065dce"},
	}, {
		name:          "greater version (patch), pre-release with build",
		actualVersion: &testVersioner{version: "1.20.0-1095+9689d22dc3121e-dirty"},
	}, {
		name:          "greater version (minor)",
		actualVersion: &testVersioner{version: "v1.20.0"},
	}, {
		name:          "same version",
		actualVersion: &testVersioner{version: "v1.20.0"},
	}, {
		name:          "same version with build",
		actualVersion: &testVersioner{version: "v1.20.0+k3s.1"},
	}, {
		name:          "same version with pre-release",
		actualVersion: &testVersioner{version: "v1.20.0-k3s.1"},
	}, {
		name:          "smaller version",
		actualVersion: &testVersioner{version: "v1.19.3"},
		wantError:     true,
	}, {
		name:          "error while fetching",
		actualVersion: &testVersioner{err: errors.New("random error")},
		wantError:     true,
	}, {
		name:          "unparseable actual version",
		actualVersion: &testVersioner{version: "v1.19.foo"},
		wantError:     true,
	}}

	minVersion := "1.20.0"

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := checkMinimumVersion(test.actualVersion, minVersion)
			if err == nil && test.wantError {
				t.Errorf("Expected an error for minimum: %q, actual: %v", minVersion, test.actualVersion)
			}

			if err != nil && !test.wantError {
				t.Errorf("Expected no error but got %v for minimum: %q, actual: %v", err, minVersion, test.actualVersion)
			}
		})
	}
}
