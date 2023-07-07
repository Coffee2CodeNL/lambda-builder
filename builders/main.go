package builders

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"

	"lambda-builder/io"

	execute "github.com/alexellis/go-execute/pkg/v1"
	extract "github.com/codeclysm/extract/v3"
	"gopkg.in/yaml.v2"
)

type Builder interface {
	Detect() bool
	Execute() error
	GetBuildImage() string
	GetConfig() Config
	GetHandlerMap() map[string]string
	Name() string
}

type Config struct {
	BuildEnv          []string
	Builder           string
	BuilderBuildImage string
	BuilderRunImage   string
	GenerateRunImage  bool
	Handler           string
	HandlerMap        map[string]string
	Identifier        string
	ImageEnv          []string
	ImageLabels       []string
	ImageTag          string
	Port              int
	RunQuiet          bool
	WorkingDirectory  string
	WriteProcfile     bool
}

func (c Config) GetImageTag() string {
	if c.ImageTag != "" {
		return c.ImageTag
	}

	appName := filepath.Base(c.WorkingDirectory)
	return fmt.Sprintf("lambda-builder/%s:latest", appName)
}

type LambdaYML struct {
	Builder    string `yaml:"builder"`
	BuildImage string `yaml:"build_image"`
	RunImage   string `yaml:"run_image"`
}

func executeBuilder(script string, config Config) error {
	if err := executeBuildContainer(script, config); err != nil {
		return err
	}

	taskHostBuildDir, err := os.MkdirTemp("", "lambda-builder")
	if err != nil {
		return fmt.Errorf("error creating build dir: %w", err)
	}

	defer func() {
		os.RemoveAll(taskHostBuildDir)
	}()

	fmt.Printf("-----> Extracting lambda.zip into build context dir\n")
	zipPath := filepath.Join(config.WorkingDirectory, "lambda.zip")
	data, _ := ioutil.ReadFile(zipPath)
	buffer := bytes.NewBuffer(data)
	if err := extract.Zip(context.Background(), buffer, taskHostBuildDir, nil); err != nil {
		return fmt.Errorf("error extracting lambda.zip into build context dir: %w", err)
	}

	handler := getFunctionHandler(taskHostBuildDir, config)
	if config.WriteProcfile && !io.FileExistsInDirectory(taskHostBuildDir, "Procfile") {
		if handler == "" {
			fmt.Printf(" !     Unable to detect handler in build directory\n")
		} else {
			fmt.Printf("=====> Writing Procfile from handler: %s\n", handler)

			fmt.Printf("       Writing to working directory\n")
			if err := writeProcfile(handler, config.WorkingDirectory); err != nil {
				return fmt.Errorf("error writing Procfile to working directory: %w", err)
			}

			fmt.Printf("       Writing to build directory\n")
			if err := writeProcfile(handler, taskHostBuildDir); err != nil {
				return fmt.Errorf("error writing Procfile to temporary build directory: %w", err)
			}
		}
	}

	if config.GenerateRunImage {
		fmt.Printf("=====> Building image\n")
		fmt.Printf("       Generating temporary Dockerfile\n")

		dockerfilePath, err := ioutil.TempFile("", "lambda-builder")
		defer func() {
			os.Remove(dockerfilePath.Name())
		}()

		if err != nil {
			return fmt.Errorf("error generating temporary Dockerfile: %w", err)
		}

		if err := generateDockerfile(handler, config, dockerfilePath); err != nil {
			return err
		}

		fmt.Printf("       Executing build of %s\n", config.GetImageTag())
		if err := buildDockerImage(taskHostBuildDir, config, dockerfilePath); err != nil {
			return err
		}
	}

	return nil
}

func executeBuildContainer(script string, config Config) error {
	args := []string{
		"container",
		"run",
		"--rm",
		"--env", "LAMBDA_BUILD_ZIP=1",
		"--label", "com.dokku.lambda-builder/executor=true",
		"--name", fmt.Sprintf("lambda-builder-executor-%s", config.Identifier),
		"--volume", fmt.Sprintf("%s:/tmp/task", config.WorkingDirectory),
	}

	for _, envPair := range config.BuildEnv {
		args = append(args, "--env", envPair)
	}
	args = append(args, config.BuilderBuildImage, "/bin/bash", "-c", script)

	cmd := execute.ExecTask{
		Args:        args,
		Command:     "docker",
		Cwd:         config.WorkingDirectory,
		StreamStdio: !config.RunQuiet,
	}

	res, err := cmd.Execute()
	if err != nil {
		return fmt.Errorf("error executing builder: %w", err)
	}

	if res.ExitCode != 0 {
		return fmt.Errorf("error executing builder, exit code %d", res.ExitCode)
	}

	return nil
}

func generateDockerfile(cmd string, config Config, dockerfilePath *os.File) error {
	tpl, err := template.New("t1").Parse(`
FROM {{ .run_image }}
{{ if ne .port "-1" }}
ENV DOCKER_LAMBDA_API_PORT={{ .port }}
ENV DOCKER_LAMBDA_RUNTIME_PORT={{ .port }}
{{ end }}
{{range .env}}
ENV {{.}}
{{end}}
{{ if ne .command "" }}
CMD ["{{ .cmd }}"]
{{ end }}
COPY . /var/task
`)
	if err != nil {
		return fmt.Errorf("error generating template: %s", err)
	}

	data := map[string]interface{}{
		"cmd":       cmd,
		"env":       config.ImageEnv,
		"port":      strconv.Itoa(config.Port),
		"run_image": config.BuilderRunImage,
	}

	if err := tpl.Execute(dockerfilePath, data); err != nil {
		return fmt.Errorf("error writing Dockerfile: %s", err)
	}

	return nil
}

func buildDockerImage(directory string, config Config, dockerfilePath *os.File) error {
	args := []string{
		"image",
		"build",
		"--file", dockerfilePath.Name(),
		"--progress", "plain",
		"--tag", config.GetImageTag(),
	}

	for _, label := range config.ImageLabels {
		args = append(args, "--label", label)
	}

	args = append(args, directory)

	cmd := execute.ExecTask{
		Args:        args,
		Command:     "docker",
		Cwd:         config.WorkingDirectory,
		StreamStdio: !config.RunQuiet,
	}

	res, err := cmd.Execute()
	if err != nil {
		return fmt.Errorf("error building image: %w", err)
	}

	if res.ExitCode != 0 {
		return fmt.Errorf("error building image, exit code %d", res.ExitCode)
	}

	return nil
}

func ParseLambdaYML(config Config) (LambdaYML, error) {
	var lambdaYML LambdaYML
	if !io.FileExistsInDirectory(config.WorkingDirectory, "lambda.yml") {
		return lambdaYML, nil
	}

	f, err := os.Open(filepath.Join(config.WorkingDirectory, "lambda.yml"))
	if err != nil {
		return lambdaYML, fmt.Errorf("error opening lambda.yml: %w", err)
	}
	defer f.Close()

	bytes, err := ioutil.ReadAll(f)
	if err != nil {
		return lambdaYML, fmt.Errorf("error reading lambda.yml: %w", err)
	}

	if err := yaml.Unmarshal(bytes, &lambdaYML); err != nil {
		return lambdaYML, fmt.Errorf("error unmarshaling lambda.yml: %w", err)
	}

	return lambdaYML, nil
}

func getBuildImage(config Config, defaultImage string) (string, error) {
	if config.BuilderBuildImage != "" {
		return config.BuilderBuildImage, nil
	}

	lambdaYML, err := ParseLambdaYML(config)
	if err != nil {
		return "", err
	}

	if lambdaYML.BuildImage == "" {
		return defaultImage, nil
	}

	return lambdaYML.BuildImage, nil
}

func getRunImage(config Config, defaultImage string) (string, error) {
	if config.BuilderRunImage != "" {
		return config.BuilderRunImage, nil
	}

	lambdaYML, err := ParseLambdaYML(config)
	if err != nil {
		return "", err
	}

	if lambdaYML.RunImage == "" {
		return defaultImage, nil
	}

	return lambdaYML.RunImage, nil
}
