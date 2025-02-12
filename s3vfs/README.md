# Virtual File System (VFS) for S3

VFS for S3 allows you to abstract away the underlying file system, and provide a uniform interface for accessing files and directories, regardless of where they are physically located.

---

- [Installation](#installation)
- [Features](#features)
- [Usage](#usage)
- [Examples](#examples)
- [Contributing](#contributing)

---

## Installation

```bash
go get oss.nandlabs.io/golly-aws/s3vfs
```

## Features

A number of features are provided out of the box.

Storage File features such as

- Read a File
- Write content to a file
- List all the files of a bucket/folder
- Get information about a file
- Add metadata to a file
- Read metadat value of a file
- Delete a file

Storage File System features such as

- Create a file, folder or a bucket
- Open a file in a given location

## Usage

The Priority of the Registered Provider is as follows

```bash
URL > HOST > Scheme("s3") > default
```

```go
package main

import (
    "context"

    "oss.nandlabs.io/golly-aws/awssvc"
)

func init() {
    config := aws.Config{
        Region: "us-east-1",
    }
    awssvc.Manager.Register("s3vfs", config)
}
```

## Examples

1. Create a bucket/file in S3

    ```go
    package main
   
    import (
        _ "oss.nandlabs.io/golly-aws/s3vfs"
        "oss.nandlabs.io/golly/vfs"
    )
   
    func main() {
        manager := vfs.GetManager()
        u, err := url.Parse("s3://bucketName")
        if err != nil {
            // handle error
        }
        file, err := manager.Create(u)

        if err != nil {
            // handle error
        }
        fmt.Println(file.Info())
    }
    ```

2. Read a file from S3
3. Delete a file in S3
4. Write a file in S3
5. List all the files in S3 bucket
6. Get File Info of an S3 object
7. Get metadata of an S3 object
8. Add metadata to an S3 object

## Contributing

We welcome contributions to the SQS library! If you find a bug, have a feature request, or want to contribute improvements, please create a pull request. For major changes, please open an issue first to discuss the changes you would like to make.
