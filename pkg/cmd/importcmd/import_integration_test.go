// +build integration

package importcmd_test

import (
	"github.com/jenkins-x-labs/jwizard/pkg/cmd/fakejxfactory"
	"github.com/jenkins-x-labs/jwizard/pkg/cmd/importcmd"
	"github.com/jenkins-x/jx/pkg/cmd/testhelpers"
	"github.com/jenkins-x/jx/pkg/config"
	"github.com/jenkins-x/jx/pkg/kube/naming"

	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"

	v1 "github.com/jenkins-x/jx/pkg/apis/jenkins.io/v1"
	fake_clients "github.com/jenkins-x/jx/pkg/cmd/clients/fake"
	"github.com/jenkins-x/jx/pkg/jenkinsfile"
	resources_test "github.com/jenkins-x/jx/pkg/kube/resources/mocks"
	"github.com/jenkins-x/jx/pkg/log"

	"github.com/jenkins-x/jx/pkg/auth"
	"github.com/jenkins-x/jx/pkg/cmd/opts"
	"github.com/jenkins-x/jx/pkg/gits"
	"github.com/jenkins-x/jx/pkg/helm"
	"github.com/jenkins-x/jx/pkg/tests"
	"github.com/jenkins-x/jx/pkg/util"
	"github.com/stretchr/testify/assert"
)

const (
	gitSuffix               = "_with_git"
	mavenKeepOldJenkinsfile = "maven_keep_old_jenkinsfile"
	mavenOldJenkinsfile     = "maven_old_jenkinsfile"
	mavenCamel              = "maven_camel"
	mavenSpringBoot         = "maven_springboot"
	probePrefix             = "probePath:"
)

func TestImportProjectsToJenkins(t *testing.T) {
	originalJxHome, tempJxHome, err := testhelpers.CreateTestJxHomeDir()
	assert.NoError(t, err)
	defer func() {
		err := testhelpers.CleanupTestJxHomeDir(originalJxHome, tempJxHome)
		assert.NoError(t, err)
	}()
	originalKubeCfg, tempKubeCfg, err := testhelpers.CreateTestKubeConfigDir()
	assert.NoError(t, err)
	defer func() {
		err := testhelpers.CleanupTestKubeConfigDir(originalKubeCfg, tempKubeCfg)
		assert.NoError(t, err)
	}()

	tempDir, err := ioutil.TempDir("", "test-import-projects")
	assert.NoError(t, err)

	testData := path.Join("test_data", "import_projects")
	_, err = os.Stat(testData)
	assert.NoError(t, err)

	files, err := ioutil.ReadDir(testData)
	assert.NoError(t, err)

	for _, f := range files {
		if f.IsDir() {
			name := f.Name()
			srcDir := filepath.Join(testData, name)
			testImportProject(t, tempDir, name, srcDir, false, "")
		}
	}
}

func TestImportProjectToJenkinsX(t *testing.T) {
	originalJxHome, tempJxHome, err := testhelpers.CreateTestJxHomeDir()
	assert.NoError(t, err)
	defer func() {
		err := testhelpers.CleanupTestJxHomeDir(originalJxHome, tempJxHome)
		assert.NoError(t, err)
	}()
	originalKubeCfg, tempKubeCfg, err := testhelpers.CreateTestKubeConfigDir()
	assert.NoError(t, err)
	defer func() {
		err := testhelpers.CleanupTestKubeConfigDir(originalKubeCfg, tempKubeCfg)
		assert.NoError(t, err)
	}()

	tempDir, err := ioutil.TempDir("", "test-import-ng-projects")
	assert.NoError(t, err)

	testData := path.Join("test_data", "import_projects")
	_, err = os.Stat(testData)
	assert.NoError(t, err)

	files, err := ioutil.ReadDir(testData)
	assert.NoError(t, err)

	for _, f := range files {
		if f.IsDir() {
			name := f.Name()
			if strings.HasPrefix(name, "maven_keep_old_jenkinsfile") {
				continue
			}
			srcDir := filepath.Join(testData, name)
			testImportProject(t, tempDir, name, srcDir, true, "")
		}
	}
}

func testImportProject(t *testing.T, tempDir string, testcase string, srcDir string, importToJenkinsX bool, buildPackURL string) {
	testDirSuffix := "jenkins"
	if importToJenkinsX {
		testDirSuffix = "jx"
	}
	testDir := filepath.Join(tempDir+"-"+testDirSuffix, testcase)
	util.CopyDir(srcDir, testDir, true)
	if strings.HasSuffix(testcase, gitSuffix) {
		gitDir := filepath.Join(testDir, ".gitdir")
		dotGitExists, gitErr := util.FileExists(gitDir)
		if gitErr != nil {
			log.Logger().Warnf("Git source directory %s does not exist: %s", gitDir, gitErr)
		} else if dotGitExists {
			dotGitDir := filepath.Join(testDir, ".git")
			util.RenameDir(gitDir, dotGitDir, true)
		}
	}
	err := assertImport(t, testDir, testcase, importToJenkinsX, "")
	assert.NoError(t, err, "Importing dir %s from source %s", testDir, srcDir)
}

func createFakeGitProvider() *gits.FakeProvider {
	testOrgName := "jstrachan"
	testRepoName := "myrepo"
	stagingRepoName := "environment-staging"
	prodRepoName := "environment-production"

	fakeRepo, _ := gits.NewFakeRepository(testOrgName, testRepoName, nil, nil)
	stagingRepo, _ := gits.NewFakeRepository(testOrgName, stagingRepoName, nil, nil)
	prodRepo, _ := gits.NewFakeRepository(testOrgName, prodRepoName, nil, nil)

	fakeGitProvider := gits.NewFakeProvider(fakeRepo, stagingRepo, prodRepo)
	userAuth := auth.UserAuth{
		Username:    "jx-testing-user",
		ApiToken:    "someapitoken",
		BearerToken: "somebearertoken",
		Password:    "password",
	}
	authServer := auth.AuthServer{
		Users:       []*auth.UserAuth{&userAuth},
		CurrentUser: userAuth.Username,
		URL:         "https://github.com",
		Kind:        gits.KindGitHub,
		Name:        "jx-testing-server",
	}
	fakeGitProvider.Server = authServer
	return fakeGitProvider
}

func assertImport(t *testing.T, testDir string, testcase string, importToJenkinsX bool, buildPackURL string) error {
	_, dirName := filepath.Split(testDir)
	dirName = naming.ToValidName(dirName)
	o := &importcmd.ImportOptions{
		CommonOptions: &opts.CommonOptions{},
	}

	o.SetFactory(fake_clients.NewFakeFactory())
	o.JXFactory = fakejxfactory.NewFakeFactory()
	o.GitProvider = createFakeGitProvider()

	k8sObjects := []runtime.Object{}
	jxObjects := []runtime.Object{}
	helmer := helm.NewHelmCLI("helm", helm.V2, dirName, true)
	testhelpers.ConfigureTestOptionsWithResources(o.CommonOptions, k8sObjects, jxObjects, gits.NewGitCLI(), nil, helmer, resources_test.NewMockInstaller())
	if o.Out == nil {
		o.Out = tests.Output()
	}
	if o.Out == nil {
		o.Out = os.Stdout
	}
	o.Dir = testDir
	o.DryRun = true
	o.DisableMaven = true
	o.UseDefaultGit = true

	if dirName == "maven-camel" {
		o.DeployKind = opts.DeployKindKnative
	}
	if importToJenkinsX {
		o.Destination.JenkinsX.Enabled = true
		callback := func(env *v1.Environment) error {
			env.Spec.TeamSettings.ImportMode = v1.ImportModeTypeYAML
			if buildPackURL != "" {
				env.Spec.TeamSettings.BuildPackURL = buildPackURL
			}
			return nil
		}
		err := o.ModifyDevEnvironment(callback)
		require.NoError(t, err, "failed to modify Dev Environment")
	} else {
		o.Destination.Jenkins.Enabled = true
		o.Destination.Jenkins.JenkinsName = "myjenkins"
		o.Destination.Jenkins.JenkinsServiceNames = []string{"myjenkins"}

		// lets generate a dummy Jenkinsfile so that we know we don't run the build packs
		jenkinsfile := filepath.Join(testDir, "Jenkinsfile")
		exists, err := util.FileExists(jenkinsfile)
		require.NoError(t, err, "could not check for file %s", jenkinsfile)
		if !exists {
			err = ioutil.WriteFile(jenkinsfile, []byte("node {}"), util.DefaultFileWritePermissions)
			require.NoError(t, err, "failed to write dummy Jenkinsfile to %s", jenkinsfile)
		}
	}

	if testcase == mavenCamel || dirName == mavenSpringBoot {
		o.DisableMaven = tests.TestShouldDisableMaven()
	}

	err := o.Run()
	assert.NoError(t, err, "Failed %s with %s", dirName, err)
	if err == nil {
		defaultJenkinsfileName := jenkinsfile.Name
		defaultJenkinsfileBackupSuffix := jenkinsfile.BackupSuffix
		defaultJenkinsfile := filepath.Join(testDir, defaultJenkinsfileName)
		jfname := defaultJenkinsfile
		if o.Jenkinsfile != "" && o.Jenkinsfile != defaultJenkinsfileName {
			jfname = filepath.Join(testDir, o.Jenkinsfile)
		}
		if dirName == "custom-jenkins" {
			tests.AssertFileExists(t, filepath.Join(testDir, jenkinsfile.Name))
			tests.AssertFileDoesNotExist(t, filepath.Join(testDir, jenkinsfile.Name+".backup"))
			tests.AssertFileDoesNotExist(t, filepath.Join(testDir, jenkinsfile.Name+"-Renamed"))
			if importToJenkinsX {
				tests.AssertFileExists(t, filepath.Join(testDir, config.ProjectConfigFileName))
			} else {
				tests.AssertFileDoesNotExist(t, filepath.Join(testDir, config.ProjectConfigFileName))
			}
		} else if importToJenkinsX {
			tests.AssertFileDoesNotExist(t, jfname)
		} else {
			tests.AssertFileExists(t, jfname)
		}

		if (dirName == "docker" || dirName == "docker-helm") && importToJenkinsX {
			tests.AssertFileExists(t, filepath.Join(testDir, "skaffold.yaml"))
		} else if dirName == "helm" || dirName == "custom-jenkins" || !importToJenkinsX {
			tests.AssertFileDoesNotExist(t, filepath.Join(testDir, "skaffold.yaml"))
		}
		if importToJenkinsX {
			if dirName == "helm" || dirName == "custom-jenkins" {
				tests.AssertFileDoesNotExist(t, filepath.Join(testDir, "Dockerfile"))
			} else {
				tests.AssertFileExists(t, filepath.Join(testDir, "Dockerfile"))
			}
		} else {
			if dirName == "docker" || dirName == "docker-helm" {
				tests.AssertFileExists(t, filepath.Join(testDir, "Dockerfile"))
			} else {
				tests.AssertFileDoesNotExist(t, filepath.Join(testDir, "Dockerfile"))
			}
		}
		if importToJenkinsX {
			if dirName == "docker" || dirName == "custom-jenkins" {
				tests.AssertFileDoesNotExist(t, filepath.Join(testDir, "charts", dirName, "Chart.yaml"))
				tests.AssertFileDoesNotExist(t, filepath.Join(testDir, "charts"))
				if !importToJenkinsX && dirName != "custom-jenkins" {
					tests.AssertFileDoesNotContain(t, jfname, "helm")
				}
			} else {
				tests.AssertFileExists(t, filepath.Join(testDir, "charts", dirName, "Chart.yaml"))
			}
		} else {
			if dirName != "helm" && dirName != "docker-helm" {
				tests.AssertFileDoesNotExist(t, filepath.Join(testDir, "charts", dirName, "Chart.yaml"))
				tests.AssertFileDoesNotExist(t, filepath.Join(testDir, "charts"))
			}
		}

		// lets test we modified the deployment kind
		if dirName == "maven-camel" {
			tests.AssertFileContains(t, filepath.Join(testDir, "charts", "maven-camel", "values.yaml"), "knativeDeploy: true")
		}
		if !importToJenkinsX {
			if strings.HasPrefix(testcase, mavenKeepOldJenkinsfile) {
				tests.AssertFileContains(t, jfname, "THIS IS OLD!")
				tests.AssertFileDoesNotExist(t, jfname+defaultJenkinsfileBackupSuffix)
			} else if strings.HasPrefix(testcase, mavenOldJenkinsfile) {
				tests.AssertFileExists(t, jfname)
			}
		}

		if !o.DisableMaven {
			if testcase == mavenCamel {
				// should have modified it
				assertProbePathEquals(t, filepath.Join(testDir, "charts", dirName, "values.yaml"), "/health")
			}
			if testcase == mavenSpringBoot {
				// should have left it
				assertProbePathEquals(t, filepath.Join(testDir, "charts", dirName, "values.yaml"), "/actuator/health")
			}
		}
	}
	return err
}

func assertProbePathEquals(t *testing.T, fileName string, expectedProbe string) {
	if tests.AssertFileExists(t, fileName) {
		data, err := ioutil.ReadFile(fileName)
		assert.NoError(t, err, "Failed to read file %s", fileName)
		if err == nil {
			text := string(data)
			found := false
			lines := strings.Split(text, "\n")

			for _, line := range lines {
				if strings.HasPrefix(line, probePrefix) {
					found = true
					value := strings.TrimSpace(strings.TrimPrefix(line, probePrefix))
					assert.Equal(t, expectedProbe, value, "file %s probe with key: %s", fileName, probePrefix)
					break
				}

			}
			assert.True(t, found, "No probe found in file %s with key: %s", fileName, probePrefix)
		}
	}
}
