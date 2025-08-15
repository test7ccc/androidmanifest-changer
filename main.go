package main

import (
	"archive/zip"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"

	"google.golang.org/protobuf/proto"
)

const (
	namespace       = "http://schemas.android.com/apk/res/android"
	versionCodeAttr = "versionCode"
	versionNameAttr = "versionName"
)

var tmpDir = os.TempDir()

type Config struct {
	versionCode int32
	versionName string
	packageName string
}

func main() {
	versionCode := flag.Uint("versionCode", 0, "The versionCode to set")
	versionName := flag.String("versionName", "", "The versionName to set")
	packageName := flag.String("package", "", "The package to set")
	flag.Parse()
	if len(flag.Args()) != 1 {
		fmt.Fprintln(flag.CommandLine.Output(), "Error: File filePath is required.")
		flag.Usage()
		os.Exit(2)
	}
	config := &Config{
		versionCode: int32(*versionCode),
		versionName: *versionName,
		packageName: *packageName,
	}

	filePath := flag.Arg(0)

	if strings.HasSuffix(filePath, ".apk") {
		updateApk(filePath, config)
	} else if strings.HasSuffix(filePath, ".aab") {
		updateAab(filePath, config)
	} else {
		updateManifest(filePath, config)
	}
}

func updateApk(path string, config *Config) {
	file, err := os.CreateTemp(tmpDir, "*.aar")
	if err != nil {
		log.Fatalln("Failed creating temp file:", err)
	}
	defer os.Remove(file.Name())

	out, err := exec.Command("aapt2", "convert", "-o", file.Name(), "--output-format", "proto", path).CombinedOutput()
	if err != nil {
		log.Fatalln("Failed executing aapt2:", err, string(out))
	}

	updateManifestPbInZip(file.Name(), "AndroidManifest.xml", config)

	out, err = exec.Command("aapt2", "convert", "-o", path, "--output-format", "binary", file.Name()).CombinedOutput()
	if err != nil {
		log.Fatalln("Failed executing aapt2:", err, string(out))
	}
}

func updateAab(path string, config *Config) {
	updateManifestPbInZip(path, "base/manifest/AndroidManifest.xml", config)
}

func updateManifestPbInZip(path string, manifestPath string, config *Config) {
	manifest, err := os.CreateTemp(tmpDir, "AndroidManifest.*.xml")
	if err != nil {
		log.Fatalln("Failed creating temp file:", err)
	}
	defer os.Remove(manifest.Name())

	extractFromZip(path, manifestPath, manifest)
	updateManifest(manifest.Name(), config)
	// 使用新的原生Go实现替代外部zip命令
	addToZipNative(path, manifestPath, manifest)
}

func extractFromZip(path string, name string, target *os.File) {
	r, err := zip.OpenReader(path)
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	f := findFile(r, name)
	if f == nil {
		log.Fatalln(errors.New("file is missing"))
	}

	innerFile, err := f.Open()
	if err != nil {
		log.Fatalln("Failed opening zip file's AndroidManifest.xml:", err)
	}
	defer innerFile.Close()
	_, err = io.Copy(target, innerFile)
	if err != nil {
		log.Fatal(err)
	}
}

func findFile(r *zip.ReadCloser, name string) *zip.File {
	for _, f := range r.File {
		if f.Name != name {
			continue
		}
		return f
	}
	return nil
}

func updateManifest(path string, config *Config) {
	in, err := os.ReadFile(path)
	if err != nil {
		log.Fatalln("Error reading file:", err)
	}

	xmlNode := &XmlNode{}
	if err := proto.Unmarshal(in, xmlNode); err != nil {
		log.Fatalln("Failed to parse manifest:", err)
	}
	for _, attr := range xmlNode.GetElement().GetAttribute() {
		if attr.GetNamespaceUri() == "" && attr.GetName() == "package" {
			if config.packageName != "" {
				fmt.Println("Changing packageName from", attr.Value, "to", config.packageName)
				attr.Value = config.packageName
			}
		}
		if attr.GetNamespaceUri() != namespace {
			continue
		}
		switch attr.GetName() {
		case versionCodeAttr:
			if config.versionCode > 0 {
				prim := attr.GetCompiledItem().GetPrim()
				if x, ok := prim.GetOneofValue().(*Primitive_IntDecimalValue); ok {
					fmt.Println("Changing versionCode from", x.IntDecimalValue, "to", config.versionCode)
					x.IntDecimalValue = int32(config.versionCode)
				}
				// In AABs the value exists, but when using aapt2 to convert the binary manifest the value is gone
				if attr.Value != "" {
					attr.Value = fmt.Sprint(config.versionCode)
				}
			}
		case versionNameAttr:
			if config.versionName != "" {
				fmt.Println("Changing versionName from", attr.Value, "to", config.versionName)
				attr.Value = config.versionName
			}
		}
	}

	// We use MarshalVT because it keeps the correct field ordering.
	// With the standard Marshal function, Android Studio can't read the resulting proto file inside aab files. :-/
	out, err := xmlNode.MarshalVT()
	if err != nil {
		log.Fatalln("Error marshalling XML:", err)
	}
	if err := os.WriteFile(path, out, 0600); err != nil {
		log.Fatalln("Error writing file:", err)
	}
}

// addToZipNative 使用Go内置zip包替代外部zip命令
// zipPath: 目标zip文件路径
// fileName: 要添加到zip中的文件名
// source: 源文件
func addToZipNative(zipPath string, fileName string, source *os.File) {
	// 读取现有zip文件的所有内容
	existingFiles := make(map[string][]byte)

	// 如果zip文件存在，先读取所有现有文件
	if _, err := os.Stat(zipPath); err == nil {
		reader, err := zip.OpenReader(zipPath)
		if err != nil {
			log.Fatalln("Failed opening zip for reading:", err)
		}
		defer reader.Close()

		for _, file := range reader.File {
			// 跳过要更新的文件
			if file.Name == fileName {
				continue
			}

			rc, err := file.Open()
			if err != nil {
				log.Fatalln("Failed opening file in zip:", err)
			}

			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				log.Fatalln("Failed reading file from zip:", err)
			}

			existingFiles[file.Name] = data
		}
	}

	// 创建新的zip文件
	zipFile, err := os.Create(zipPath)
	if err != nil {
		log.Fatalln("Failed creating zip file:", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// 写入所有现有文件
	for name, data := range existingFiles {
		writer, err := zipWriter.Create(name)
		if err != nil {
			log.Fatalln("Failed creating file in zip:", err)
		}

		_, err = writer.Write(data)
		if err != nil {
			log.Fatalln("Failed writing file to zip:", err)
		}
	}

	// 添加新文件
	writer, err := zipWriter.Create(fileName)
	if err != nil {
		log.Fatalln("Failed creating new file in zip:", err)
	}

	source.Seek(0, 0)
	_, err = io.Copy(writer, source)
	if err != nil {
		log.Fatalln("Failed copying file to zip:", err)
	}
}
