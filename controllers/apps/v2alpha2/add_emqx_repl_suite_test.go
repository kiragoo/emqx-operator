package v2alpha2

import (
	"context"
	"fmt"
	"sort"
	"time"

	appsv2alpha2 "github.com/emqx/emqx-operator/apis/apps/v2alpha2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Check add repl controller", Ordered, Label("repl"), func() {
	var a *addRepl
	var instance *appsv2alpha2.EMQX = new(appsv2alpha2.EMQX)
	var ns *corev1.Namespace = &corev1.Namespace{}

	BeforeEach(func() {
		a = &addRepl{emqxReconciler}

		ns = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "controller-v2alpha2-add-emqx-repl-test",
				Labels: map[string]string{
					"test": "e2e",
				},
			},
		}

		instance = emqx.DeepCopy()
		instance.Namespace = ns.Name
		instance.Spec.ReplicantTemplate = &appsv2alpha2.EMQXReplicantTemplate{
			Spec: appsv2alpha2.EMQXReplicantTemplateSpec{
				Replicas: pointer.Int32(3),
			},
		}
		instance.Status = appsv2alpha2.EMQXStatus{
			ReplicantNodesStatus: &appsv2alpha2.EMQXNodesStatus{
				Replicas: 3,
			},
			Conditions: []metav1.Condition{
				{
					Type:               appsv2alpha2.Ready,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Time{Time: time.Now().AddDate(0, 0, -1)},
				},
				{
					Type:               appsv2alpha2.CoreNodesReady,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Time{Time: time.Now().AddDate(0, 0, -1)},
				},
			},
		}
		instance.Default()
	})

	It("create namespace", func() {
		Expect(k8sClient.Create(context.TODO(), ns)).Should(Succeed())
	})

	Context("replicant template is nil", func() {
		JustBeforeEach(func() {
			instance.Spec.ReplicantTemplate = nil
		})

		It("should do nothing", func() {
			Eventually(a.reconcile(ctx, instance, nil)).Should(Equal(subResult{}))
			Eventually(func() []appsv1.ReplicaSet {
				list := &appsv1.ReplicaSetList{}
				_ = k8sClient.List(ctx, list,
					client.InNamespace(instance.Namespace),
					client.MatchingLabels(instance.Labels),
				)
				return list.Items
			}).Should(HaveLen(0))
		})
	})

	Context("core nodes is not ready", func() {
		JustBeforeEach(func() {
			instance.Status.RemoveCondition(appsv2alpha2.CoreNodesReady)
		})

		It("should do nothing", func() {
			Eventually(a.reconcile(ctx, instance, nil)).Should(Equal(subResult{}))
			Eventually(func() []appsv1.ReplicaSet {
				list := &appsv1.ReplicaSetList{}
				_ = k8sClient.List(ctx, list,
					client.InNamespace(instance.Namespace),
					client.MatchingLabels(instance.Spec.ReplicantTemplate.Labels),
				)
				return list.Items
			}).Should(HaveLen(0))
		})
	})

	Context("replicant template is not nil, and core code is ready", func() {
		It("should create replicaSet", func() {
			Eventually(a.reconcile(ctx, instance, nil)).Should(Equal(subResult{}))
			Eventually(func() []appsv1.ReplicaSet {
				list := &appsv1.ReplicaSetList{}
				_ = k8sClient.List(ctx, list,
					client.InNamespace(instance.Namespace),
					client.MatchingLabels(instance.Spec.ReplicantTemplate.Labels),
				)
				return list.Items
			}).Should(ConsistOf(
				WithTransform(func(rs appsv1.ReplicaSet) string { return rs.Spec.Template.Spec.Containers[0].Image }, Equal(instance.Spec.Image)),
			))
		})
	})

	Context("scale down replicas count", func() {
		JustBeforeEach(func() {
			list := &appsv1.ReplicaSetList{}
			Eventually(func() []appsv1.ReplicaSet {
				_ = k8sClient.List(ctx, list,
					client.InNamespace(instance.Namespace),
					client.MatchingLabels(instance.Spec.ReplicantTemplate.Labels),
				)
				return list.Items
			}).Should(HaveLen(1))
			instance.Status.ReplicantNodesStatus.CurrentRevision = list.Items[0].Labels[appsv2alpha2.PodTemplateHashLabelKey]

			instance.Spec.ReplicantTemplate.Spec.Replicas = pointer.Int32(0)
		})

		It("should update replicaSet", func() {
			Eventually(a.reconcile(ctx, instance, nil)).Should(Equal(subResult{}))
			Eventually(func() []appsv1.ReplicaSet {
				list := &appsv1.ReplicaSetList{}
				_ = k8sClient.List(ctx, list,
					client.InNamespace(instance.Namespace),
					client.MatchingLabels(instance.Spec.ReplicantTemplate.Labels),
				)
				return list.Items
			}).Should(ConsistOf(
				WithTransform(func(rs appsv1.ReplicaSet) int32 { return *rs.Spec.Replicas }, Equal(*instance.Spec.ReplicantTemplate.Spec.Replicas)),
			))
		})
	})

	Context("scale up replicas count", func() {
		JustBeforeEach(func() {
			list := &appsv1.ReplicaSetList{}
			Eventually(func() []appsv1.ReplicaSet {
				_ = k8sClient.List(ctx, list,
					client.InNamespace(instance.Namespace),
					client.MatchingLabels(instance.Spec.ReplicantTemplate.Labels),
				)
				return list.Items
			}).Should(HaveLen(1))
			instance.Status.ReplicantNodesStatus.CurrentRevision = list.Items[0].Labels[appsv2alpha2.PodTemplateHashLabelKey]

			instance.Spec.ReplicantTemplate.Spec.Replicas = pointer.Int32(4)
		})

		It("should update replicaSet", func() {
			Eventually(a.reconcile(ctx, instance, nil)).Should(Equal(subResult{}))
			Eventually(func() []appsv1.ReplicaSet {
				list := &appsv1.ReplicaSetList{}
				_ = k8sClient.List(ctx, list,
					client.InNamespace(instance.Namespace),
					client.MatchingLabels(instance.Spec.ReplicantTemplate.Labels),
				)
				return list.Items
			}).Should(ConsistOf(
				WithTransform(func(rs appsv1.ReplicaSet) int32 { return *rs.Spec.Replicas }, Equal(*instance.Spec.ReplicantTemplate.Spec.Replicas)),
			))

			Eventually(func() *appsv2alpha2.EMQX {
				_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)
				return instance
			}).Should(WithTransform(
				func(emqx *appsv2alpha2.EMQX) string { return emqx.Status.GetLastTrueCondition().Type }, Equal(appsv2alpha2.ReplicantNodesProgressing),
			))
		})
	})

	Context("change image", func() {
		JustBeforeEach(func() {
			instance.Spec.Image = "emqx/emqx"
			instance.Spec.UpdateStrategy.InitialDelaySeconds = int32(999999999)
		})

		It("should create new replicaSet", func() {
			Eventually(a.reconcile(ctx, instance, nil)).Should(Equal(subResult{}))
			Eventually(func() []appsv1.ReplicaSet {
				list := &appsv1.ReplicaSetList{}
				_ = k8sClient.List(ctx, list,
					client.InNamespace(instance.Namespace),
					client.MatchingLabels(instance.Spec.ReplicantTemplate.Labels),
				)
				return list.Items
			}).Should(ConsistOf(
				WithTransform(func(rs appsv1.ReplicaSet) string { return rs.Spec.Template.Spec.Containers[0].Image }, Equal(emqx.Spec.Image)),
				WithTransform(func(rs appsv1.ReplicaSet) string { return rs.Spec.Template.Spec.Containers[0].Image }, Equal(instance.Spec.Image)),
			))

			Eventually(func() *appsv2alpha2.EMQX {
				_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)
				return instance
			}).Should(WithTransform(
				func(emqx *appsv2alpha2.EMQX) string { return emqx.Status.GetLastTrueCondition().Type }, Equal(appsv2alpha2.ReplicantNodesProgressing),
			))
		})
	})

	Context("can be scale down", func() {
		var old, new *appsv1.ReplicaSet = new(appsv1.ReplicaSet), new(appsv1.ReplicaSet)

		JustBeforeEach(func() {
			list := &appsv1.ReplicaSetList{}
			Eventually(func() []appsv1.ReplicaSet {
				_ = k8sClient.List(ctx, list,
					client.InNamespace(instance.Namespace),
					client.MatchingLabels(instance.Spec.ReplicantTemplate.Labels),
				)
				sort.Slice(list.Items, func(i, j int) bool {
					return list.Items[i].CreationTimestamp.Before(&list.Items[j].CreationTimestamp)
				})
				return list.Items
			}).Should(HaveLen(2))
			old = list.Items[0].DeepCopy()
			new = list.Items[1].DeepCopy()

			fakeOldPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: old.Name + "-",
					Namespace:    old.Namespace,
					Labels:       old.Spec.Template.Labels,
					OwnerReferences: []metav1.OwnerReference{
						*metav1.NewControllerRef(old, appsv1.SchemeGroupVersion.WithKind("ReplicaSet")),
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "emqx", Image: old.Spec.Template.Spec.Containers[0].Image},
					},
				},
			}
			Expect(k8sClient.Create(ctx, fakeOldPod)).Should(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(fakeOldPod), fakeOldPod)).Should(Succeed())
			fakeOldPod.Status.PodIP = "1.2.3.4"
			Expect(k8sClient.Status().Update(ctx, fakeOldPod)).Should(Succeed())

			instance.Spec.Image = new.Spec.Template.Spec.Containers[0].Image
			instance.Status.ReplicantNodesStatus.CurrentRevision = new.Labels[appsv2alpha2.PodTemplateHashLabelKey]
			instance.Status.ReplicantNodesStatus.Nodes = []appsv2alpha2.EMQXNode{
				{
					Node:    fmt.Sprintf("emqx@%s", fakeOldPod.Status.PodIP),
					Edition: "Enterprise",
					Session: 0,
				},
			}

			instance.Spec.UpdateStrategy.InitialDelaySeconds = int32(0)
			instance.Spec.UpdateStrategy.EvacuationStrategy.WaitTakeover = int32(0)
		})
		It("should scale down", func() {
			for *old.Spec.Replicas > 0 {
				preReplicas := *old.Spec.Replicas
				//mock statefulSet status
				old.Status.Replicas = preReplicas
				old.Status.ReadyReplicas = preReplicas
				Expect(k8sClient.Status().Update(ctx, old)).Should(Succeed())
				Eventually(func() *appsv1.ReplicaSet {
					_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(old), old)
					return old
				}).WithTimeout(timeout).WithPolling(interval).Should(And(
					WithTransform(func(s *appsv1.ReplicaSet) int32 { return s.Status.Replicas }, Equal(preReplicas)),
					WithTransform(func(s *appsv1.ReplicaSet) int32 { return s.Status.ReadyReplicas }, Equal(preReplicas)),
				))

				// retry it because update the replicaSet maybe will conflict
				Eventually(a.reconcile(ctx, instance, nil)).WithTimeout(timeout).WithPolling(interval).Should(Equal(subResult{}))
				_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(old), old)
				Expect(*old.Spec.Replicas).Should(Equal(preReplicas - 1))
			}
		})
	})

	It("delete namespace", func() {
		Expect(k8sClient.Delete(ctx, ns)).Should(Succeed())
	})
})
