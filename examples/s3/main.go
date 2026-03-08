package main

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"oss.nandlabs.io/golly-aws/awscfg"
	_ "oss.nandlabs.io/golly-aws/s3"
	"oss.nandlabs.io/golly/vfs"
)

func main() {
	region := envOrDefault("AWS_REGION", "us-east-1")
	bucket := envOrDefault("BUCKET_NAME", "golly-s3vfs-example")
	endpoint := os.Getenv("S3_ENDPOINT")

	// Configure AWS
	cfg := awscfg.NewConfig(region)
	if endpoint != "" {
		cfg.SetEndpoint(endpoint)
		cfg.SetStaticCredentials("test", "test", "")
		fmt.Printf("Using custom endpoint: %s\n", endpoint)
	}
	awscfg.Manager.Register("s3", cfg)

	mgr := vfs.GetManager()
	base := fmt.Sprintf("s3://%s/s3vfs-demo", bucket)

	fmt.Println("=== s3vfs Comprehensive Example ===")
	fmt.Printf("Bucket: %s | Region: %s\n\n", bucket, region)

	// 1. Create a directory
	step("1. Create directory")
	dir, err := mgr.MkdirRaw(base + "/data/")
	check(err, "MkdirRaw")
	_ = dir.Close()

	// 2. Write files
	step("2. Write files")
	writeFile(mgr, base+"/data/hello.txt", "Hello from golly s3vfs!")
	writeFile(mgr, base+"/data/config.json", `{"app":"golly","version":"1.0"}`)
	writeFile(mgr, base+"/data/notes.md", "# Notes\n\nA markdown file.")
	writeFile(mgr, base+"/data/reports/summary.csv", "name,value\nmetric_a,100\nmetric_b,200")
	writeFile(mgr, base+"/data/reports/detail.csv", "id,name,score\n1,Alice,95\n2,Bob,87")
	writeFile(mgr, base+"/data/logs/app.log", "2026-02-26 INFO Application started")

	// 3. Read file as string
	step("3. Read file as string")
	readAndPrint(mgr, base+"/data/hello.txt")

	// 4. Read file as bytes
	step("4. Read file as bytes")
	f, err := mgr.OpenRaw(base + "/data/config.json")
	check(err, "OpenRaw config.json")
	data, err := f.AsBytes()
	check(err, "AsBytes")
	_ = f.Close()
	fmt.Printf("  config.json (%d bytes): %s\n", len(data), string(data))

	// 5. File info
	step("5. File info")
	f, err = mgr.OpenRaw(base + "/data/hello.txt")
	check(err, "OpenRaw hello.txt")
	info, err := f.Info()
	check(err, "Info")
	_ = f.Close()
	fmt.Printf("  Name=%s Size=%d ModTime=%s IsDir=%t\n",
		info.Name(), info.Size(), info.ModTime(), info.IsDir())

	// 6. List directory
	step("6. List directory")
	files, err := mgr.ListRaw(base + "/data/")
	check(err, "ListRaw")
	for _, fl := range files {
		fi, _ := fl.Info()
		fmt.Printf("  %s (dir=%t)\n", fi.Name(), fi.IsDir())
	}

	// 7. Walk entire tree
	step("7. Walk tree")
	err = mgr.WalkRaw(base+"/data/", func(file vfs.VFile) error {
		fi, _ := file.Info()
		fmt.Printf("  %s\n", fi.Name())
		return nil
	})
	check(err, "WalkRaw")

	// 8. Find CSV files
	step("8. Find CSV files")
	loc, _ := url.Parse(base + "/data/")
	csvFiles, err := mgr.Find(loc, func(file vfs.VFile) (bool, error) {
		fi, e := file.Info()
		if e != nil {
			return false, e
		}
		return strings.HasSuffix(fi.Name(), ".csv"), nil
	})
	check(err, "Find")
	for _, cf := range csvFiles {
		fmt.Printf("  %s\n", cf.Url().String())
	}

	// 9. Metadata properties
	step("9. Metadata properties")
	f, err = mgr.OpenRaw(base + "/data/hello.txt")
	check(err, "OpenRaw metadata")
	check(f.AddProperty("department", "engineering"), "AddProperty")
	fmt.Println("  Set department=engineering")
	dept, err := f.GetProperty("department")
	check(err, "GetProperty")
	fmt.Printf("  Got department=%s\n", dept)
	_ = f.Close()

	// 10. Copy file
	step("10. Copy file")
	err = mgr.CopyRaw(base+"/data/hello.txt", base+"/backup/hello-copy.txt")
	check(err, "CopyRaw")
	fmt.Println("  Copied hello.txt -> backup/hello-copy.txt")
	readAndPrint(mgr, base+"/backup/hello-copy.txt")

	// 11. Move file
	step("11. Move file")
	err = mgr.MoveRaw(base+"/data/notes.md", base+"/archive/notes.md")
	check(err, "MoveRaw")
	fmt.Println("  Moved notes.md -> archive/notes.md")
	readAndPrint(mgr, base+"/archive/notes.md")

	// 12. Parent navigation
	step("12. Parent navigation")
	f, err = mgr.OpenRaw(base + "/data/reports/summary.csv")
	check(err, "OpenRaw parent")
	parent, err := f.Parent()
	check(err, "Parent")
	fmt.Printf("  Child:  %s\n", f.Url())
	fmt.Printf("  Parent: %s\n", parent.Url())
	_ = f.Close()
	_ = parent.Close()

	// 13. Delete matching (.log files)
	step("13. Delete matching (.log files)")
	logLoc, _ := url.Parse(base + "/data/")
	err = mgr.DeleteMatching(logLoc, func(file vfs.VFile) (bool, error) {
		fi, e := file.Info()
		if e != nil {
			return false, e
		}
		return strings.HasSuffix(fi.Name(), ".log"), nil
	})
	check(err, "DeleteMatching")
	fmt.Println("  Deleted all .log files")

	// 14. Cleanup
	step("14. Cleanup")
	err = mgr.DeleteRaw(base + "/")
	check(err, "DeleteRaw cleanup")
	fmt.Println("  Cleaned up all demo data")

	fmt.Println("\n=== Example complete ===")
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func check(err error, ctx string) {
	if err != nil {
		log.Fatalf("%s: %v", ctx, err)
	}
}

func step(name string) {
	fmt.Printf("\n--- %s ---\n", name)
}

func writeFile(mgr vfs.Manager, path, content string) {
	f, err := mgr.CreateRaw(path)
	check(err, "CreateRaw "+path)
	_, err = f.WriteString(content)
	check(err, "WriteString "+path)
	check(f.Close(), "Close "+path)
	fmt.Printf("  Created: %s (%d bytes)\n", path, len(content))
}

func readAndPrint(mgr vfs.Manager, path string) {
	f, err := mgr.OpenRaw(path)
	check(err, "OpenRaw "+path)
	s, err := f.AsString()
	check(err, "AsString "+path)
	_ = f.Close()
	fmt.Printf("  Content: %q\n", s)
}
