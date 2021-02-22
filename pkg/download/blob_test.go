package download

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/storage"
	"github.com/koralski/run-command-extension-linux/pkg/blobutil"
	"github.com/stretchr/testify/require"
)

func Test_blobDownload_validateInputs(t *testing.T) {
	type sas interface {
		getURL() (string, error)
	}

	_, err := NewBlobDownload("", "", blobutil.AzureBlobRef{}).GetRequest()
	require.NotNil(t, err)
	errorMessage := err.Error()
	require.Contains(t, errorMessage, "failed to initialize azure storage client")
	require.Contains(t, errorMessage, "azure: account name is not valid")

	_, err = NewBlobDownload("account", "", blobutil.AzureBlobRef{}).GetRequest()
	require.NotNil(t, err)
	require.Contains(t, err.Error(), "failed to initialize azure storage client")
	require.Contains(t, err.Error(), "account key required")

	_, err = NewBlobDownload("account", "Zm9vCg==", blobutil.AzureBlobRef{}).GetRequest()
	require.NotNil(t, err)
	require.Contains(t, err.Error(), "failed to initialize azure storage client")

	_, err = NewBlobDownload("account", "Zm9vCg==", blobutil.AzureBlobRef{
		StorageBase: storage.DefaultBaseURL,
	}).GetRequest()
	require.Nil(t, err)
}

func Test_blobDownload_getURL(t *testing.T) {
	type sas interface {
		getURL() (string, error)
	}

	d := NewBlobDownload("account", "Zm9vCg==", blobutil.AzureBlobRef{
		StorageBase: "test.core.windows.net",
		Container:   "",
		Blob:        "blob.txt",
	})

	v, ok := d.(blobDownload)
	require.True(t, ok)

	url, err := v.getURL()
	require.Nil(t, err)
	require.Contains(t, url, "https://", "missing https scheme")
	require.Contains(t, url, "/account.blob.test.core.windows.net/", "missing/wrong host")
	require.Contains(t, url, "/$root/", "missing container in url")
	require.Contains(t, url, "/blob.txt", "missing blob name in url")
	for _, v := range []string{"sig", "se", "sr", "sp", "sv"} { // SAS query parameters
		require.Contains(t, url, v+"=", "missing SAS query '%s' in url", v)
	}
}

func Test_blobDownload_fails_badCreds(t *testing.T) {
	d := NewBlobDownload("example", "Zm9vCg==", blobutil.AzureBlobRef{
		StorageBase: storage.DefaultBaseURL,
		Blob:        "fooBlob.txt",
		Container:   "foocontainer",
	})

	status, _, err := Download(d)
	require.NotNil(t, err)
	require.Contains(t, err.Error(), "unexpected status code: actual=403")
	require.Equal(t, status, http.StatusForbidden)
}

func Test_blobDownload_fails_urlNotFound(t *testing.T) {
	d := NewBlobDownload("accountname", "Zm9vCg==", blobutil.AzureBlobRef{
		StorageBase: ".example.com",
		Blob:        "fooBlob.txt",
		Container:   "foocontainer",
	})

	_, _, err := Download(d)
	require.NotNil(t, err)
	require.Contains(t, err.Error(), "http request failed:")
}

func Test_blobDownload_actualBlob(t *testing.T) {
	acct := os.Getenv("AZURE_STORAGE_ACCOUNT")
	key := os.Getenv("AZURE_STORAGE_ACCESS_KEY")
	if acct == "" || key == "" {
		t.Skipf("Skipping: AZURE_STORAGE_ACCOUNT or AZURE_STORAGE_ACCESS_KEY not specified to run this test")
	}
	base := storage.DefaultBaseURL

	// Create a blob first
	client, err := storage.NewClient(acct, key, base, storage.DefaultAPIVersion, true)
	require.Nil(t, err)
	blobStorageClient := client.GetBlobService()

	var (
		n         = 1024 * 64
		name      = "blob.txt"
		container = fmt.Sprintf("run-command-test-%d", rand.New(rand.NewSource(time.Now().UnixNano())).Int63())
		chunk     = make([]byte, n)
	)

	containerReference := blobStorageClient.GetContainerReference(container)
	_, err = containerReference.DeleteIfExists(nil)
	require.Nil(t, err)
	_, err = containerReference.CreateIfNotExists(&storage.CreateContainerOptions{Access: storage.ContainerAccessTypePrivate})
	require.Nil(t, err)
	defer containerReference.Delete(nil)
	blobReference := containerReference.GetBlobReference(name)
	require.Nil(t, blobReference.PutAppendBlob(nil))
	rand.Read(chunk)
	require.Nil(t, blobReference.AppendBlock(chunk, nil))

	// Get the blob via downloader
	d := NewBlobDownload(acct, key, blobutil.AzureBlobRef{
		Container:   container,
		Blob:        name,
		StorageBase: base,
	})
	_, body, err := Download(d)
	require.Nil(t, err)
	defer body.Close()
	b, err := ioutil.ReadAll(body)
	require.Nil(t, err)
	require.EqualValues(t, chunk, b, "retrieved body is different body=%d chunk=%d", len(b), len(chunk))
}
