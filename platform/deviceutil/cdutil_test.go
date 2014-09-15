package deviceutil_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	fakecdrom "github.com/cloudfoundry/bosh-agent/platform/cdrom/fakes"
	fakesys "github.com/cloudfoundry/bosh-agent/system/fakes"

	. "github.com/cloudfoundry/bosh-agent/platform/deviceutil"
)

var _ = Describe("Cdutil", func() {
	var (
		fs     *fakesys.FakeFileSystem
		cdrom  *fakecdrom.FakeCdrom
		cdutil DeviceUtil
	)

	BeforeEach(func() {
		fs = fakesys.NewFakeFileSystem()
		cdrom = fakecdrom.NewFakeCdrom(fs, "env", "fake env contents")
	})

	JustBeforeEach(func() {
		cdutil = NewCdUtil("/fake/settings/dir", fs, cdrom)
	})

	It("gets file contents from CDROM", func() {
		contents, err := cdutil.GetFileContents("env")
		Expect(err).NotTo(HaveOccurred())

		Expect(cdrom.Mounted).To(Equal(false))
		Expect(cdrom.MediaAvailable).To(Equal(false))
		Expect(fs.FileExists("/fake/settings/dir")).To(Equal(true))
		Expect(cdrom.MountMountPath).To(Equal("/fake/settings/dir"))

		Expect(contents).To(Equal([]byte("fake env contents")))
	})

})