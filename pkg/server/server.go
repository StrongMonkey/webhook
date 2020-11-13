package server

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/rancher/dynamiclistener"
	"github.com/rancher/dynamiclistener/server"
	"github.com/rancher/webhook/pkg/clients"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

var (
	namespace           = "cattle-system"
	tlsName             = "rancher-webhook.cattle-system.svc"
	certName            = "cattle-webhook-tls"
	caName              = "cattle-webhook-ca"
	port                = int32(443)
	validationPath      = "/v1/webhook/validation"
	mutationPath        = "/v1/webhook/mutation"
	clusterScope        = v1.ClusterScope
	namespaceScope      = v1.NamespacedScope
	failPolicyFail      = v1.Fail
	failPolicyIgnore    = v1.Ignore
	sideEffectClassNone = v1.SideEffectClassNone
)

func ListenAndServe(ctx context.Context, cfg *rest.Config) error {
	clients, err := clients.New(cfg)
	if err != nil {
		return err
	}

	validation, err := Validation(clients)
	if err != nil {
		return err
	}

	mutation, err := Mutation(clients)
	if err != nil {
		return err
	}

	router := mux.NewRouter()
	router.Handle(validationPath, validation)
	router.Handle(mutationPath, mutation)

	return listenAndServe(ctx, clients, router)
}

func listenAndServe(ctx context.Context, clients *clients.Clients, handler http.Handler) (rErr error) {
	apply := clients.Apply.WithDynamicLookup()

	clients.Core.Secret().OnChange(ctx, "secrets", func(key string, secret *corev1.Secret) (*corev1.Secret, error) {
		if secret == nil || secret.Name != caName || secret.Namespace != namespace || len(secret.Data[corev1.TLSCertKey]) == 0 {
			return nil, nil
		}

		logrus.Info("Sleeping for 15 seconds then applying webhook config")
		// Sleep here to make sure server is listening and all caches are primed
		time.Sleep(15 * time.Second)

		return secret, apply.WithOwner(secret).ApplyObjects(&v1.ValidatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rancher.cattle.io",
			},
			Webhooks: []v1.ValidatingWebhook{
				{
					Name: "rancher.cattle.io",
					ClientConfig: v1.WebhookClientConfig{
						Service: &v1.ServiceReference{
							Namespace: namespace,
							Name:      "rancher-webhook",
							Path:      &validationPath,
							Port:      &port,
						},
						CABundle: secret.Data[corev1.TLSCertKey],
					},
					Rules: []v1.RuleWithOperations{
						{
							Operations: []v1.OperationType{
								v1.Create,
								v1.Update,
							},
							Rule: v1.Rule{
								APIGroups:   []string{"management.cattle.io"},
								APIVersions: []string{"v3"},
								Resources:   []string{"clusters"},
								Scope:       &clusterScope,
							},
						},
					},
					FailurePolicy:           &failPolicyIgnore,
					SideEffects:             &sideEffectClassNone,
					AdmissionReviewVersions: []string{"v1", "v1beta1"},
				},
				{
					Name: "rancherauth.cattle.io",
					ClientConfig: v1.WebhookClientConfig{
						Service: &v1.ServiceReference{
							Namespace: namespace,
							Name:      "rancher-webhook",
							Path:      &validationPath,
							Port:      &port,
						},
						CABundle: secret.Data[corev1.TLSCertKey],
					},
					Rules: []v1.RuleWithOperations{
						{
							Operations: []v1.OperationType{
								v1.Create,
								v1.Update,
								v1.Delete,
							},
							Rule: v1.Rule{
								APIGroups:   []string{"management.cattle.io"},
								APIVersions: []string{"v3"},
								Resources:   []string{"globalrolebindings"},
								Scope:       &clusterScope,
							},
						},
						{
							Operations: []v1.OperationType{
								v1.Create,
								v1.Update,
							},
							Rule: v1.Rule{
								APIGroups:   []string{"management.cattle.io"},
								APIVersions: []string{"v3"},
								Resources:   []string{"roletemplates"},
								Scope:       &clusterScope,
							},
						},
						{
							Operations: []v1.OperationType{
								v1.Create,
								v1.Update,
							},
							Rule: v1.Rule{
								APIGroups:   []string{"management.cattle.io"},
								APIVersions: []string{"v3"},
								Resources:   []string{"projectroletemplatebindings"},
								Scope:       &namespaceScope,
							},
						},
						{
							Operations: []v1.OperationType{
								v1.Create,
								v1.Update,
							},
							Rule: v1.Rule{
								APIGroups:   []string{"management.cattle.io"},
								APIVersions: []string{"v3"},
								Resources:   []string{"clusterroletemplatebindings"},
								Scope:       &namespaceScope,
							},
						},
					},
					FailurePolicy:           &failPolicyFail,
					SideEffects:             &sideEffectClassNone,
					AdmissionReviewVersions: []string{"v1", "v1beta1"},
				},
			},
		}, &v1.MutatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rancher.cattle.io",
			},
			Webhooks: []v1.MutatingWebhook{
				{
					Name: "rancher.cattle.io",
					ClientConfig: v1.WebhookClientConfig{
						Service: &v1.ServiceReference{
							Namespace: namespace,
							Name:      "rancher-webhook",
							Path:      &mutationPath,
							Port:      &port,
						},
						CABundle: secret.Data[corev1.TLSCertKey],
					},
					Rules: []v1.RuleWithOperations{
						{
							Operations: []v1.OperationType{
								v1.Create,
							},
							Rule: v1.Rule{
								APIGroups:   []string{"management.cattle.io"},
								APIVersions: []string{"v3"},
								Resources:   []string{"fleetworkspaces"},
								Scope:       &clusterScope,
							},
						},
					},
					FailurePolicy:           &failPolicyFail,
					SideEffects:             &sideEffectClassNone,
					AdmissionReviewVersions: []string{"v1", "v1beta1"},
				},
			},
		})
	})

	return server.ListenAndServe(ctx, 9443, 0, handler, &server.ListenOpts{
		Secrets:       clients.Core.Secret(),
		CertNamespace: namespace,
		CertName:      certName,
		CAName:        caName,
		TLSListenerConfig: dynamiclistener.Config{
			SANs: []string{
				tlsName,
			},
			FilterCN: dynamiclistener.OnlyAllow(tlsName),
		},
	})
}
