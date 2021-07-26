// Copyright (c) 2020 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package shoot_test

import (
	"time"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	. "github.com/gardener/gardener/pkg/controllermanager/controller/shoot"
	"github.com/gardener/gardener/pkg/logger"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

var _ = Describe("Shoot Maintenance", func() {
	now := time.Now()
	expirationDateInTheFuture := metav1.Time{Time: now.Add(time.Minute * 10)}
	expirationDateInThePast := metav1.Time{Time: now.AddDate(0, 0, -1)}

	Context("Shoot Maintenance", func() {
		Describe("ExpirationDateExpired", func() {
			It("should determine that expirationDate applies", func() {
				applies := ExpirationDateExpired(&expirationDateInThePast)
				Expect(applies).To(Equal(true))
			})

			It("should determine that expirationDate not applies", func() {
				applies := ExpirationDateExpired(&expirationDateInTheFuture)
				Expect(applies).To(Equal(false))
			})
		})

		Describe("ForceMachineImageUpdateRequired", func() {
			var (
				shootCurrentImage = &gardencorev1beta1.ShootMachineImage{
					Name:    "CoreOs",
					Version: pointer.String("1.0.0"),
				}
			)

			It("should determine that forceUpdate is required", func() {
				imageCloudProvider := gardencorev1beta1.MachineImage{
					Name: "CoreOs",
					Versions: []gardencorev1beta1.MachineImageVersion{
						{
							ExpirableVersion: gardencorev1beta1.ExpirableVersion{
								Version: "1.0.1",
							},
						},
						{
							ExpirableVersion: gardencorev1beta1.ExpirableVersion{
								Version:        "1.0.0",
								ExpirationDate: &expirationDateInThePast,
							},
						},
					},
				}

				required := ForceMachineImageUpdateRequired(shootCurrentImage, imageCloudProvider)
				Expect(required).To(Equal(true))
			})

			It("should determine that forceUpdate is not required", func() {
				imageCloudProvider := gardencorev1beta1.MachineImage{
					Name: "CoreOs",
					Versions: []gardencorev1beta1.MachineImageVersion{
						{
							ExpirableVersion: gardencorev1beta1.ExpirableVersion{
								Version: "1.0.1",
							},
						},
						{
							ExpirableVersion: gardencorev1beta1.ExpirableVersion{
								Version:        "1.0.0",
								ExpirationDate: &expirationDateInTheFuture,
							},
						},
					},
				}

				required := ForceMachineImageUpdateRequired(shootCurrentImage, imageCloudProvider)
				Expect(required).To(Equal(false))
			})
		})
	})

	Describe("MaintainMachineImages", func() {
		var (
			shootCurrentImage        *gardencorev1beta1.ShootMachineImage
			cloudProfile             *gardencorev1beta1.CloudProfile
			shoot                    *gardencorev1beta1.Shoot
			testlogger               = logger.NewFieldLogger(logger.NewLogger("info"), "cloudprofile", "test")
			previewClassification    = gardencorev1beta1.ClassificationPreview
			deprecatedClassification = gardencorev1beta1.ClassificationDeprecated
		)

		BeforeEach(func() {
			shootCurrentImage = &gardencorev1beta1.ShootMachineImage{
				Name:    "CoreOs",
				Version: pointer.String("1.0.0"),
			}

			cloudProfile = &gardencorev1beta1.CloudProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "profile",
				},
				Spec: gardencorev1beta1.CloudProfileSpec{
					MachineImages: []gardencorev1beta1.MachineImage{
						{
							Name: "CoreOs",
							Versions: []gardencorev1beta1.MachineImageVersion{
								{
									ExpirableVersion: gardencorev1beta1.ExpirableVersion{
										Version: "1.0.0",
									},
								},
								{
									ExpirableVersion: gardencorev1beta1.ExpirableVersion{
										Version:        "1.1.1",
										ExpirationDate: &expirationDateInTheFuture,
									},
								},
							},
						},
					},
				},
			}

			shoot = &gardencorev1beta1.Shoot{
				ObjectMeta: metav1.ObjectMeta{
					Name: "shoot",
				},
				Spec: gardencorev1beta1.ShootSpec{
					Maintenance: &gardencorev1beta1.Maintenance{
						AutoUpdate: &gardencorev1beta1.MaintenanceAutoUpdate{
							MachineImageVersion: true,
						},
					},
					Provider: gardencorev1beta1.Provider{Workers: []gardencorev1beta1.Worker{
						{
							Name:    "cpu-worker",
							Machine: gardencorev1beta1.Machine{Image: shootCurrentImage},
						},
					},
					},
				},
			}
		})

		It("should determine that the shoot worker machine images must be maintained - ForceUpdate (expiration in the past & expired status)", func() {
			shoot.Spec.Maintenance.AutoUpdate.MachineImageVersion = false
			cloudProfile.Spec.MachineImages[0].Versions[0].ExpirationDate = &expirationDateInThePast

			_, err := MaintainMachineImages(testlogger, shoot, cloudProfile)

			Expect(err).NotTo(HaveOccurred())
			assertWorkerMachineImageVersion(&shoot.Spec.Provider.Workers[0], "CoreOs", "1.1.1")
		})

		It("should determine that the shoot worker machine images must be maintained - MaintenanceAutoUpdate set to true (nil is also is being defaulted to true in the API server)", func() {
			_, err := MaintainMachineImages(testlogger, shoot, cloudProfile)

			Expect(err).NotTo(HaveOccurred())
			assertWorkerMachineImageVersion(&shoot.Spec.Provider.Workers[0], "CoreOs", "1.1.1")
		})

		It("should determine that the shoot worker machine images must be maintained - multiple worker pools", func() {
			cloudProfile.Spec.MachineImages = append(cloudProfile.Spec.MachineImages, gardencorev1beta1.MachineImage{
				Name: "gardenlinux",
				Versions: []gardencorev1beta1.MachineImageVersion{
					{
						ExpirableVersion: gardencorev1beta1.ExpirableVersion{
							Version: "1.0.0",
						},
					},
				},
			})

			otherWorker := gardencorev1beta1.Worker{
				Name: "cpu-glinux",
				Machine: gardencorev1beta1.Machine{Image: &gardencorev1beta1.ShootMachineImage{
					Name:    "gardenlinux",
					Version: pointer.String("1.0.0"),
				}},
			}

			shoot.Spec.Provider.Workers = append(shoot.Spec.Provider.Workers, otherWorker)
			_, err := MaintainMachineImages(testlogger, shoot, cloudProfile)

			Expect(err).NotTo(HaveOccurred())
			assertWorkerMachineImageVersion(&shoot.Spec.Provider.Workers[0], "CoreOs", "1.1.1")
			assertWorkerMachineImageVersion(&shoot.Spec.Provider.Workers[1], "gardenlinux", "1.0.0")
		})

		It("should update to latest non-preview version - MaintenanceAutoUpdate set to true", func() {
			highestPreviewVersion := gardencorev1beta1.MachineImageVersion{
				ExpirableVersion: gardencorev1beta1.ExpirableVersion{
					Version:        "1.1.2",
					Classification: &previewClassification,
				},
			}
			cloudProfile.Spec.MachineImages[0].Versions = append(cloudProfile.Spec.MachineImages[0].Versions, highestPreviewVersion)

			_, err := MaintainMachineImages(testlogger, shoot, cloudProfile)

			Expect(err).NotTo(HaveOccurred())
			assertWorkerMachineImageVersion(&shoot.Spec.Provider.Workers[0], "CoreOs", "1.1.1")
		})

		It("should determine that the shoot worker machine images must NOT to be maintained - ForceUpdate not required & MaintenanceAutoUpdate set to false", func() {
			shoot.Spec.Maintenance.AutoUpdate.MachineImageVersion = false

			expected := shoot.Spec.Provider.Workers[0].Machine.Image.DeepCopy()
			_, err := MaintainMachineImages(testlogger, shoot, cloudProfile)

			Expect(err).NotTo(HaveOccurred())
			Expect(shoot.Spec.Provider.Workers[0].Machine.Image).To(Equal(expected))
		})

		It("should determine that the shoot worker machine images must NOT to be maintained - already on latest qualifying machine image version.", func() {
			highestVersion := "1.1.1"
			cloudProfile.Spec.MachineImages = []gardencorev1beta1.MachineImage{
				{
					Name: "CoreOs",
					Versions: []gardencorev1beta1.MachineImageVersion{
						{
							ExpirableVersion: gardencorev1beta1.ExpirableVersion{
								Version: "1.0.1",
							},
						},
						{
							ExpirableVersion: gardencorev1beta1.ExpirableVersion{
								Version: highestVersion,
							},
						},
					},
				},
			}
			shoot.Spec.Provider.Workers[0].Machine.Image.Version = &highestVersion
			expected := shoot.Spec.Provider.Workers[0].Machine.Image.DeepCopy()
			_, err := MaintainMachineImages(testlogger, shoot, cloudProfile)
			Expect(err).NotTo(HaveOccurred())
			Expect(shoot.Spec.Provider.Workers[0].Machine.Image).To(Equal(expected))
		})

		It("should determine that the shoot worker machine images must NOT be maintained - found no machineImageVersion with matching CRI", func() {
			cloudProfile.Spec.MachineImages[0].Versions[0].CRI = []gardencorev1beta1.CRI{{Name: gardencorev1beta1.CRINameContainerD}}
			shoot.Spec.Provider.Workers[0].CRI = &gardencorev1beta1.CRI{Name: gardencorev1beta1.CRINameContainerD}

			expected := shoot.Spec.Provider.Workers[0].Machine.Image.DeepCopy()
			_, err := MaintainMachineImages(testlogger, shoot, cloudProfile)
			Expect(err).NotTo(HaveOccurred())
			Expect(shoot.Spec.Provider.Workers[0].Machine.Image).To(Equal(expected))
		})

		It("should determine that some shoot worker machine images must be not be maintained - MachineImageVersion doesn't support certain CRIs", func() {
			cloudProfile.Spec.MachineImages[0].Versions[0].CRI = []gardencorev1beta1.CRI{{Name: gardencorev1beta1.CRINameContainerD}}
			shoot.Spec.Provider.Workers[0].CRI = &gardencorev1beta1.CRI{Name: gardencorev1beta1.CRINameContainerD}

			shoot.Spec.Provider.Workers = append(shoot.Spec.Provider.Workers, gardencorev1beta1.Worker{Name: "worker-without-cri-config", Machine: gardencorev1beta1.Machine{Image: shootCurrentImage}})

			_, err := MaintainMachineImages(testlogger, shoot, cloudProfile)
			Expect(err).NotTo(HaveOccurred())

			assertWorkerMachineImageVersion(&shoot.Spec.Provider.Workers[0], "CoreOs", "1.0.0")
			assertWorkerMachineImageVersion(&shoot.Spec.Provider.Workers[1], "CoreOs", "1.1.1")
		})

		It("should determine that the shoot worker machine images must be maintained - cloud profile has no matching (machineImage.name & machineImage.version) machine image defined (the shoots image has been deleted from the cloudProfile) -> update to latest machineImage with same name", func() {
			cloudProfile.Spec.MachineImages = []gardencorev1beta1.MachineImage{
				{
					Name: "CoreOs",
					Versions: []gardencorev1beta1.MachineImageVersion{
						{
							ExpirableVersion: gardencorev1beta1.ExpirableVersion{
								Version:        "1.1.1",
								ExpirationDate: &expirationDateInTheFuture,
							},
						},
					},
				},
			}

			_, err := MaintainMachineImages(testlogger, shoot, cloudProfile)

			Expect(err).NotTo(HaveOccurred())
			assertWorkerMachineImageVersion(&shoot.Spec.Provider.Workers[0], "CoreOs", "1.1.1")
		})

		It("should determine that the Shoot is already using the latest qualifying version - Shoot is using a preview version (and there is no higher non-preview version).", func() {
			highestExpiredVersion := gardencorev1beta1.MachineImageVersion{
				ExpirableVersion: gardencorev1beta1.ExpirableVersion{
					Version:        "1.1.2",
					Classification: &previewClassification,
				},
			}
			cloudProfile.Spec.MachineImages[0].Versions = append(cloudProfile.Spec.MachineImages[0].Versions, highestExpiredVersion)
			shoot.Spec.Provider.Workers[0].Machine.Image.Version = &highestExpiredVersion.Version
			_, err := MaintainMachineImages(testlogger, shoot, cloudProfile)

			Expect(err).NotTo(HaveOccurred())
			assertWorkerMachineImageVersion(&shoot.Spec.Provider.Workers[0], "CoreOs", "1.1.2")
		})

		It("should return an error - cloud profile has no matching (machineImage.name) machine image defined", func() {
			cloudProfile.Spec.MachineImages = cloudProfile.Spec.MachineImages[1:]

			_, err := MaintainMachineImages(testlogger, shoot, cloudProfile)

			Expect(err).NotTo(BeNil())
		})

		It("should return an error - edge case: qualifying version from CloudProfile for machine image is lower than the Shoot's version. Should not downgrade shoot machine image version.", func() {
			highestExpiredVersion := gardencorev1beta1.MachineImageVersion{
				ExpirableVersion: gardencorev1beta1.ExpirableVersion{
					Version:        "1.1.2",
					Classification: &deprecatedClassification,
					ExpirationDate: &expirationDateInThePast,
				},
			}
			cloudProfile.Spec.MachineImages[0].Versions = append(cloudProfile.Spec.MachineImages[0].Versions, highestExpiredVersion)
			shoot.Spec.Provider.Workers[0].Machine.Image.Version = &highestExpiredVersion.Version
			_, err := MaintainMachineImages(testlogger, shoot, cloudProfile)

			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Maintain Kubernetes Version", func() {
		var (
			cloudProfile          *gardencorev1beta1.CloudProfile
			shoot                 *gardencorev1beta1.Shoot
			kubernetesSettings    gardencorev1beta1.KubernetesSettings
			previewClassification = gardencorev1beta1.ClassificationPreview
			shootLogger           = logger.NewShootLogger(logger.NewLogger(""), "mock", "mockspace")
		)

		BeforeEach(func() {
			kubernetesSettings = gardencorev1beta1.KubernetesSettings{
				Versions: []gardencorev1beta1.ExpirableVersion{
					{
						Version: "1.1.2",
					},
					{
						Version: "1.1.1",
					},
					{
						Version: "1.1.0",
					},
					{
						Version: "1.0.2",
					},
					{
						Version: "1.0.1",
					},
					{
						Version:        "1.0.0",
						ExpirationDate: &expirationDateInTheFuture,
					},
					{
						Version: "2.0.0",
					},
				},
			}
			cloudProfile = &gardencorev1beta1.CloudProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "profile",
				},
				Spec: gardencorev1beta1.CloudProfileSpec{
					Kubernetes: kubernetesSettings,
				},
			}

			shoot = &gardencorev1beta1.Shoot{
				ObjectMeta: metav1.ObjectMeta{
					Name: "shoot",
				},
				Spec: gardencorev1beta1.ShootSpec{
					Maintenance: &gardencorev1beta1.Maintenance{
						AutoUpdate: &gardencorev1beta1.MaintenanceAutoUpdate{
							KubernetesVersion: true,
						},
					},
					Kubernetes: gardencorev1beta1.Kubernetes{Version: "1.0.0"},
				},
			}
		})
		It("should determine that the shoot kubernetes version must be maintained - ForceUpdate to latest patch version", func() {
			shoot.Spec.Maintenance.AutoUpdate.KubernetesVersion = false
			cloudProfile.Spec.Kubernetes.Versions[4].ExpirationDate = &expirationDateInThePast
			shoot.Spec.Kubernetes = gardencorev1beta1.Kubernetes{Version: "1.0.1"}

			version, _, err := MaintainKubernetesVersion(shoot, cloudProfile, shootLogger)

			Expect(err).NotTo(HaveOccurred())
			Expect(version).NotTo(BeNil())
			Expect(*version).To(Equal("1.0.2"))
		})

		It("should determine that the shoot kubernetes version must be maintained - ForceUpdate to latest non-preview patch version", func() {
			shoot.Spec.Maintenance.AutoUpdate.KubernetesVersion = false
			// expire shoots kubernetes version 1.0.0
			cloudProfile.Spec.Kubernetes.Versions[5].ExpirationDate = &expirationDateInThePast
			shoot.Spec.Kubernetes = gardencorev1beta1.Kubernetes{Version: "1.0.0"}

			// mark latest version 1.02 as preview
			cloudProfile.Spec.Kubernetes.Versions[3].Classification = &previewClassification

			version, _, err := MaintainKubernetesVersion(shoot, cloudProfile, shootLogger)

			Expect(err).To(Not(HaveOccurred()))
			Expect(version).NotTo(BeNil())
			Expect(*version).To(Equal("1.0.1"))
		})

		It("should determine that the shoot kubernetes version must be maintained - ForceUpdate to latest qualifying patch version of next minor version", func() {
			shoot.Spec.Maintenance.AutoUpdate.KubernetesVersion = false
			cloudProfile.Spec.Kubernetes.Versions[3].ExpirationDate = &expirationDateInThePast
			shoot.Spec.Kubernetes = gardencorev1beta1.Kubernetes{Version: "1.0.2"}

			version, _, err := MaintainKubernetesVersion(shoot, cloudProfile, shootLogger)

			Expect(err).NotTo(HaveOccurred())
			Expect(version).NotTo(BeNil())
			Expect(*version).To(Equal("1.1.2"))
		})

		It("should determine that the shoot kubernetes version must be maintained - ForceUpdate to latest qualifying patch version of next minor version", func() {
			shoot.Spec.Maintenance.AutoUpdate.KubernetesVersion = true
			cloudProfile.Spec.Kubernetes.Versions[3].ExpirationDate = &expirationDateInThePast
			shoot.Spec.Kubernetes = gardencorev1beta1.Kubernetes{Version: "1.0.2"}

			version, _, err := MaintainKubernetesVersion(shoot, cloudProfile, shootLogger)

			Expect(err).NotTo(HaveOccurred())
			Expect(version).NotTo(BeNil())
			Expect(*version).To(Equal("1.1.2"))
		})

		// special case when all the patch versions of the consecutive minor versions are expired
		It("should determine that the shoot kubernetes version must be maintained - ForceUpdate to latest qualifying patch version (is expired) of next minor version.", func() {
			shoot.Spec.Maintenance.AutoUpdate.KubernetesVersion = false
			// expire version 1.0.2
			cloudProfile.Spec.Kubernetes.Versions[3].ExpirationDate = &expirationDateInThePast
			shoot.Spec.Kubernetes = gardencorev1beta1.Kubernetes{Version: "1.0.2"}

			// expire all the version of the consecutive minor version
			cloudProfile.Spec.Kubernetes.Versions[0].ExpirationDate = &expirationDateInThePast
			cloudProfile.Spec.Kubernetes.Versions[1].ExpirationDate = &expirationDateInThePast
			cloudProfile.Spec.Kubernetes.Versions[2].ExpirationDate = &expirationDateInThePast

			version, _, err := MaintainKubernetesVersion(shoot, cloudProfile, shootLogger)

			Expect(err).NotTo(HaveOccurred())
			Expect(version).NotTo(BeNil())
			Expect(*version).To(Equal("1.1.2"))
		})

		It("should determine that the shoot kubernetes version must be maintained - however the ForceUpdate is impossible (only preview version available)", func() {
			shoot.Spec.Maintenance.AutoUpdate.KubernetesVersion = false
			cloudProfile.Spec.Kubernetes.Versions[0].Classification = &previewClassification
			cloudProfile.Spec.Kubernetes.Versions[1].Classification = &previewClassification
			cloudProfile.Spec.Kubernetes.Versions[2].Classification = &previewClassification

			cloudProfile.Spec.Kubernetes.Versions[3].ExpirationDate = &expirationDateInThePast
			shoot.Spec.Kubernetes = gardencorev1beta1.Kubernetes{Version: "1.0.2"}

			_, _, err := MaintainKubernetesVersion(shoot, cloudProfile, shootLogger)

			Expect(err).To(HaveOccurred())
		})

		It("should determine that the shoot kubernetes version must be maintained - MaintenanceAutoUpdate set to true", func() {
			shoot.Spec.Maintenance.AutoUpdate.KubernetesVersion = true
			shoot.Spec.Kubernetes = gardencorev1beta1.Kubernetes{Version: "1.0.1"}

			version, _, err := MaintainKubernetesVersion(shoot, cloudProfile, shootLogger)

			Expect(err).NotTo(HaveOccurred())
			Expect(version).NotTo(BeNil())
			Expect(*version).To(Equal("1.0.2"))
		})

		It("should determine that the kubernetes version must NOT to be maintained - ForceUpdate not required & MaintenanceAutoUpdate set to false", func() {
			shoot.Spec.Maintenance.AutoUpdate.KubernetesVersion = false
			cloudProfile.Spec.Kubernetes.Versions[4].ExpirationDate = &expirationDateInTheFuture
			shoot.Spec.Kubernetes = gardencorev1beta1.Kubernetes{Version: "1.0.1"}

			version, _, err := MaintainKubernetesVersion(shoot, cloudProfile, shootLogger)

			Expect(err).NotTo(HaveOccurred())
			Expect(version).To(BeNil())
		})

		It("should determine that the shootKubernetes version must be maintained - cloud profile has no matching kubernetes version defined (the shoots kubernetes version has been deleted from the cloudProfile) -> update to latest kubernetes patch version with same minor", func() {
			cloudProfile.Spec.Kubernetes.Versions = kubernetesSettings.Versions[:4]
			shoot.Spec.Maintenance.AutoUpdate.KubernetesVersion = true
			shoot.Spec.Kubernetes = gardencorev1beta1.Kubernetes{Version: "1.0.0"}

			version, _, err := MaintainKubernetesVersion(shoot, cloudProfile, shootLogger)

			Expect(err).NotTo(HaveOccurred())
			Expect(version).NotTo(BeNil())
			Expect(*version).To(Equal("1.0.2"))
		})

		It("should determine that the shootKubernetes version must be maintained - cloud profile has no matching kubernetes version defined (the shoots kubernetes version has been deleted from the cloudProfile) && isLatest patch version for minor-> update to latest kubernetes patch version for next minor", func() {
			cloudProfile.Spec.Kubernetes.Versions = kubernetesSettings.Versions[:2]
			shoot.Spec.Maintenance.AutoUpdate.KubernetesVersion = true
			shoot.Spec.Kubernetes = gardencorev1beta1.Kubernetes{Version: "1.0.2"}

			version, _, err := MaintainKubernetesVersion(shoot, cloudProfile, shootLogger)

			Expect(err).NotTo(HaveOccurred())
			Expect(version).NotTo(BeNil())
			Expect(*version).To(Equal("1.1.2"))
		})

		It("do not update major Kubernetes version", func() {
			shoot.Spec.Maintenance.AutoUpdate.KubernetesVersion = false
			cloudProfile.Spec.Kubernetes.Versions[3].ExpirationDate = &expirationDateInThePast
			shoot.Spec.Kubernetes = gardencorev1beta1.Kubernetes{Version: "1.1.2"}

			version, _, err := MaintainKubernetesVersion(shoot, cloudProfile, shootLogger)

			Expect(err).NotTo(HaveOccurred())
			Expect(version).To(BeNil())
		})
	})
})

func assertWorkerMachineImageVersion(worker *gardencorev1beta1.Worker, imageName string, imageVersion string) {
	ExpectWithOffset(1, worker.Machine.Image.Name).To(Equal(imageName))
	ExpectWithOffset(1, *worker.Machine.Image.Version).To(Equal(imageVersion))
}
