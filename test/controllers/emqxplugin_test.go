/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/emqx/emqx-operator/apis/apps/v1beta3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	//+kubebuilder:scaffold:imports
)

var _ = Describe("", func() {
	Context("Check default plugin", func() {
		It("Check default plugin", func() {
			for _, emqx := range emqxList() {
				pluginList := []string{"emqx_management", "emqx_dashboard", "emqx_rule_engine", "emqx_retainer"}
				loaded_plugins := "emqx_management.\nemqx_dashboard.\nemqx_retainer.\nemqx_rule_engine.\n"
				if _, ok := emqx.(*v1beta3.EmqxEnterprise); ok {
					pluginList = append(pluginList, "emqx_modules")
					loaded_plugins += "emqx_modules.\n"
				}
				Eventually(func() map[string]string {
					cm := &corev1.ConfigMap{}
					_ = k8sClient.Get(
						context.Background(),
						types.NamespacedName{
							Name:      fmt.Sprintf("%s-%s", emqx.GetName(), "loaded-plugins"),
							Namespace: emqx.GetNamespace(),
						}, cm,
					)
					return cm.Data
				}, timeout, interval).Should(Equal(
					map[string]string{
						"loaded_plugins": loaded_plugins,
					},
				))

				for _, pluginName := range pluginList {
					Eventually(func() error {
						plugin := &v1beta3.EmqxPlugin{}
						err := k8sClient.Get(
							context.Background(),
							types.NamespacedName{
								Name:      strings.ReplaceAll(strings.Replace(pluginName, "emqx", emqx.GetName(), 1), "_", "-"),
								Namespace: emqx.GetNamespace(),
							}, plugin,
						)
						return err
					}, timeout, interval).Should(Succeed())

					Eventually(func() map[string]string {
						cm := &corev1.ConfigMap{}
						_ = k8sClient.Get(
							context.Background(),
							types.NamespacedName{
								Name:      fmt.Sprintf("%s-%s", emqx.GetName(), "plugins-config"),
								Namespace: emqx.GetNamespace(),
							}, cm,
						)
						return cm.Data
					}, timeout, interval).Should(HaveKey(pluginName + ".conf"))
				}
			}
		})
	})

	Context("Check lwm2m plugin", func() {
		lwm2mPorts := []corev1.ServicePort{
			{
				Name:       "lwm2m-bind-udp-1",
				Protocol:   corev1.ProtocolUDP,
				Port:       5683,
				TargetPort: intstr.FromInt(5683),
			},
			{
				Name:       "lwm2m-bind-udp-2",
				Protocol:   corev1.ProtocolUDP,
				Port:       5684,
				TargetPort: intstr.FromInt(5684),
			},
			{
				Name:       "lwm2m-bind-dtls-1",
				Protocol:   corev1.ProtocolUDP,
				Port:       5685,
				TargetPort: intstr.FromInt(5685),
			},
			{
				Name:       "lwm2m-bind-dtls-2",
				Protocol:   corev1.ProtocolUDP,
				Port:       5686,
				TargetPort: intstr.FromInt(5686),
			},
		}

		It("Check create plugin", func() {
			for _, emqx := range emqxList() {
				// Create plugin
				lwm2m := &v1beta3.EmqxPlugin{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "apps.emqx.io/v1beta3",
						Kind:       "EmqxPlugin",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-%s", emqx.GetName(), "lwm2m"),
						Namespace: emqx.GetNamespace(),
					},
					Spec: v1beta3.EmqxPluginSpec{
						PluginName: "emqx_lwm2m",
						Selector:   emqx.GetLabels(),
						Config: map[string]string{
							"lwm2m.lifetime_min": "1s",
							"lwm2m.lifetime_max": "86400s",
							"lwm2m.bind.udp.1":   "0.0.0.0:5683",
							"lwm2m.bind.udp.2":   "0.0.0.0:5684",
							"lwm2m.bind.dtls.1":  "0.0.0.0:5685",
							"lwm2m.bind.dtls.2":  "0.0.0.0:5686",
							"lwm2m.xml_dir":      "/opt/emqx/etc/lwm2m_xml",
						},
					},
				}
				Expect(k8sClient.Create(context.Background(), lwm2m)).Should(Succeed())

				Eventually(func() error {
					plugin := &v1beta3.EmqxPlugin{}
					err := k8sClient.Get(
						context.Background(),
						types.NamespacedName{
							Name:      fmt.Sprintf("%s-%s", emqx.GetName(), "lwm2m"),
							Namespace: emqx.GetNamespace(),
						}, plugin,
					)
					return err
				}, timeout, interval).Should(Succeed())

				Eventually(func() string {
					cm := &corev1.ConfigMap{}
					_ = k8sClient.Get(
						context.Background(),
						types.NamespacedName{
							Name:      fmt.Sprintf("%s-%s", emqx.GetName(), "loaded-plugins"),
							Namespace: emqx.GetNamespace(),
						}, cm,
					)
					return cm.Data["loaded_plugins"]
				}, timeout, interval).Should(ContainSubstring("emqx_lwm2m."))

				Eventually(func() map[string]string {
					pluginConfig := &corev1.ConfigMap{}
					_ = k8sClient.Get(
						context.Background(),
						types.NamespacedName{
							Name:      fmt.Sprintf("%s-%s", emqx.GetName(), "plugins-config"),
							Namespace: emqx.GetNamespace(),
						}, pluginConfig,
					)
					return pluginConfig.Data
				}, timeout, interval).Should(HaveKey("emqx_lwm2m.conf"))

				Eventually(func() []corev1.ServicePort {
					svc := &corev1.Service{}
					_ = k8sClient.Get(
						context.Background(),
						types.NamespacedName{
							Name:      emqx.GetName(),
							Namespace: emqx.GetNamespace(),
						},
						svc,
					)
					return svc.Spec.Ports
				}).Should(ContainElements(lwm2mPorts))
			}
		})

		It("Check update plugin", func() {
			for _, emqx := range emqxList() {
				// Update plugin
				Eventually(func() error {
					plugin := &v1beta3.EmqxPlugin{}
					err := k8sClient.Get(
						context.Background(),
						types.NamespacedName{
							Name:      fmt.Sprintf("%s-%s", emqx.GetName(), "lwm2m"),
							Namespace: emqx.GetNamespace(),
						}, plugin,
					)
					if err != nil {
						return err
					}
					plugin.Spec.Config["lwm2m.bind.udp.1"] = "5687"
					plugin.Spec.Config["lwm2m.bind.udp.2"] = "5688"
					return k8sClient.Update(context.Background(), plugin)
				}, timeout, interval).Should(Succeed())
				Eventually(func() []corev1.ServicePort {
					svc := &corev1.Service{}
					_ = k8sClient.Get(
						context.Background(),
						types.NamespacedName{
							Name:      emqx.GetName(),
							Namespace: emqx.GetNamespace(),
						},
						svc,
					)
					return svc.Spec.Ports
				}, timeout, interval).Should(ContainElements([]corev1.ServicePort{
					{
						Name:       "lwm2m-bind-udp-1",
						Protocol:   corev1.ProtocolUDP,
						Port:       5687,
						TargetPort: intstr.FromInt(5687),
					},
					{
						Name:       "lwm2m-bind-udp-2",
						Protocol:   corev1.ProtocolUDP,
						Port:       5688,
						TargetPort: intstr.FromInt(5688),
					},
				}))
			}
		})

		It("Check delete plugin", func() {
			for _, emqx := range emqxList() {
				// Delete plugin
				Expect(k8sClient.Delete(
					context.Background(),
					&v1beta3.EmqxPlugin{
						ObjectMeta: metav1.ObjectMeta{
							Name:      fmt.Sprintf("%s-%s", emqx.GetName(), "lwm2m"),
							Namespace: emqx.GetNamespace(),
						},
					},
				)).Should(Succeed())

				Eventually(func() string {
					cm := &corev1.ConfigMap{}
					_ = k8sClient.Get(
						context.Background(),
						types.NamespacedName{
							Name:      fmt.Sprintf("%s-%s", emqx.GetName(), "loaded-plugins"),
							Namespace: emqx.GetNamespace(),
						}, cm,
					)
					return cm.Data["loaded_plugins"]
				}, timeout, interval).ShouldNot(ContainSubstring("{emqx_lwm2m, true}"))

				Eventually(func() map[string]string {
					cm := &corev1.ConfigMap{}
					_ = k8sClient.Get(
						context.Background(),
						types.NamespacedName{
							Name:      fmt.Sprintf("%s-%s", emqx.GetName(), "plugins-config"),
							Namespace: emqx.GetNamespace(),
						}, cm,
					)
					return cm.Data
				}, timeout, interval).ShouldNot(HaveKey("emqx_lwm2m.conf"))

				Eventually(func() []corev1.ServicePort {
					svc := &corev1.Service{}
					_ = k8sClient.Get(
						context.Background(),
						types.NamespacedName{
							Name:      emqx.GetName(),
							Namespace: emqx.GetNamespace(),
						},
						svc,
					)
					return svc.Spec.Ports
				}).ShouldNot(ContainElements(lwm2mPorts))
			}
		})
	})
})