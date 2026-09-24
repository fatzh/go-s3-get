/*
Copyright © 2026 Fabrice Tereszkiewicz - A/Z&T <fabrice@azt.ch>

Check https://sacules.github.io/post/adventures-go-tui-1/ for inspiration
*/

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/spf13/cobra"
	"gopkg.in/ini.v1"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
  "github.com/awsdocs/aws-doc-sdk-examples/gov2/s3/actions"
	"github.com/aws/smithy-go"
)

// store the sections in the credential file
var (
  credentials []*ini.Section
  app *tview.Application
)

const searchModalPage = "*searchModalPage*"
const mainViewPage = "*mainViewPage*"
const objectViewPage = "*objectViewPage"

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "s3get",
	Short: "Simple CLI to get a file from a configured S3 compatible storage.",
	Long: `Use this tool to browse files on a S3 compatible bucket.`,
  Run: func(cmd *cobra.Command, args []string) { 
    app = tview.NewApplication()

    var (
      // grid layout
      mainView = tview.NewGrid()

      // pages
      pages = tview.NewPages()

      // in each table, the rows can be selected (but not the columns)
      credentialsView = tview.NewTable().SetSelectable(true, false)
      bucketsView = tview.NewTable().SetSelectable(true, false)
      contentView = tview.NewTable().SetSelectable(true, false)

      // title / current dir
      cwdInfo = tview.NewTextView()
      // status with download link/size
      fileInfo = tview.NewTextView()

      // search field for prefix search
      searchField = tview.NewInputField().
        SetFieldBackgroundColor(tcell.ColorLightPink)

      // objecct text info
      objectText = tview.NewTextView().
        SetLabel("Object info").
        SetTextAlign(tview.AlignLeft).
        SetDynamicColors(true)

      // prefix search, to be reset after search
      prefix string

      // s3 stuff
      s3Client *s3.Client
      ctx context.Context
    )

    // display a list of available S3 profiles in the credentialsView table
    for i, cred := range credentials {
      cell := tview.NewTableCell(cred.Name())
      cell.SetExpansion(1)
      // add it to the table
      credentialsView.SetCell(i, 0, cell)
    }

    // selecting a credentials should load the buckets for this account
    listBuckets := func(row, column int) {
      credentialsSelected := credentialsView.GetCell(row, 0).Text
      cwdInfo.SetText(credentialsSelected)
      // initialise profile
      ctx = context.Background()
      cfg, err := config.LoadDefaultConfig(ctx, 
        config.WithSharedConfigProfile(credentialsSelected))

      if err != nil {
        panic(err)
      }

      s3Client = s3.NewFromConfig(cfg, func(o *s3.Options) {
        o.Region = "eu-central-1"  // not sure why... I don't think it matters for S3
      })

      // clear bucket list
      bucketsView.Clear()
      // and unselect
      bucketsView.SetSelectable(false, false)

      // load buckets using credentials
      result, err := s3Client.ListBuckets(ctx, &s3.ListBucketsInput{})

      if err != nil {
        var ae smithy.APIError
        if errors.As(err, &ae) && ae.ErrorCode() == "AccessDenied" {
          fileInfo.SetText("You don't have permission to list buckets for this account.")
        } else {
          fileInfo.SetText("Error")  // how do I get the error info here?
        }
        return
      } else {
        fileInfo.Clear()
      }

      // 100 buckets... should be enough
      count := 100
      if len(result.Buckets) == 0 {
        fmt.Println("You don't have any buckets!")
      } else {
        if count > len(result.Buckets) {
          count = len(result.Buckets)
        }
        for i, bucket := range result.Buckets[:count] {
          cell := tview.NewTableCell(*bucket.Name)
          cell.SetExpansion(1)
          bucketsView.SetCell(i, 0, cell)
        }
      }
      bucketsView.ScrollToBeginning()

    }

    listBucketContent := func(row, column int) {
      // clear content
      contentView.Clear()
      bucketName := bucketsView.GetCell(row, 0).Text
      cwdInfo.SetText(bucketName)
      var (
        err error
        input *s3.ListObjectsV2Input
        output *s3.ListObjectsV2Output
        objects []types.Object
      )
      if prefix != "" {
        input = &s3.ListObjectsV2Input{
          Bucket: aws.String(bucketName),
          Prefix: aws.String(prefix),
        }
        prefix = ""
      } else {
        input = &s3.ListObjectsV2Input{
          Bucket: aws.String(bucketName),
        }
      }
      objectPaginator := s3.NewListObjectsV2Paginator(s3Client, input)
      fileInfo.SetText("Loading files...")
      // just one page...
      output, err = objectPaginator.NextPage(ctx)
      if err != nil {
        var noBucket *types.NoSuchBucket
        if errors.As(err, &noBucket) {
          log.Printf("Bucket %s does not exist.\n", bucketName)
          err = noBucket
        }
      } else {
        objects = append(objects, output.Contents...)
      }

      // count objects
      objectsCountInfo := strconv.Itoa(len(objects))
      if len(objects) > 0 && objectPaginator.HasMorePages() {
        objectsCountInfo += "+"
      }

      fileInfo.SetText(objectsCountInfo + " files")

      // list them here
      for i, object := range objects {
          cell := tview.NewTableCell(*object.Key)
          cell.SetExpansion(1)
          contentView.SetCell(i, 0, cell)
      }
      contentView.SetSelectable(false, false)
      contentView.ScrollToBeginning()
      app.SetFocus(bucketsView)
    }

    getObjectInfo := func(row, column int) {
      objectKey := contentView.GetCell(row, 0).Text
      cwdInfo.SetText(objectKey)
    }

    // plug the list bucket call to the credentialsView
    credentialsView.SetSelectionChangedFunc(listBuckets)

    // plug the list bucket content to the bucketsView
    bucketsView.SetSelectionChangedFunc(listBucketContent)

    // plug the object info to the contentView
    contentView.SetSelectionChangedFunc(getObjectInfo)

    // change/move in the profile list
    credentialsView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
      if event.Key() == tcell.KeyRight {
        // handle UI
        bucketsView.SetSelectable(true, false)
        app.SetFocus(bucketsView)
        bucketsView.Select(0, 0)
        contentView.Clear()
        contentView.SetSelectable(false, false)
        r, _ := bucketsView.GetSelection()
        currentBucket := bucketsView.GetCell(r, 0).Text
        cwdInfo.SetText(currentBucket)
      }
      if event.Rune() == 'q' {
        app.Stop()

      }
      return event
    })

    // change/move in the bucket list
    bucketsView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
      switch key := event.Key(); key {
      case tcell.KeyRight:
        app.SetFocus(contentView)
        contentView.SetSelectable(true, false)
        contentView.Select(0, 0)
        r, _ := contentView.GetSelection()
        currentFile := contentView.GetCell(r, 0).Text
        cwdInfo.SetText(currentFile)
      case tcell.KeyLeft:
        app.SetFocus(credentialsView)
        contentView.Clear()
        contentView.SetSelectable(false, false)
        bucketsView.SetSelectable(false, false)
        // and update current location
        credentialsSelected := credentialsView.GetCell(credentialsView.GetSelection()).Text
        cwdInfo.SetText(credentialsSelected)
      }
      // if event.Rune() == '/' {
      //   searchField.SetText(prefix)
      //   pages.ShowPage(searchModalPage)
      //   app.SetFocus(searchField)
      // }
      return event
    })

    // change/move in the file list
    contentView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
      switch key := event.Key(); key {
      case tcell.KeyLeft :
        app.SetFocus(bucketsView)
        contentView.SetSelectable(false, false)
        // and update the current location
        bucketSelected := bucketsView.GetCell(bucketsView.GetSelection()).Text
        cwdInfo.SetText(bucketSelected)
      case tcell.KeyEnter :
        bucketSelected := bucketsView.GetCell(bucketsView.GetSelection()).Text
        objectSelected := contentView.GetCell(contentView.GetSelection()).Text
        // get a download url
        presignClient := s3.NewPresignClient(s3Client)
        presigner := actions.Presigner{PresignClient: presignClient}
        presignedGetRequest, err := presigner.GetObject(ctx, bucketSelected, objectSelected, 60)
        if err != nil {
          log.Println("Failed to sign the request", err)
        }
        objectText.SetText("\n\nBucket: " + bucketSelected + "\nKey: " + objectSelected + "\n\n[:::" + presignedGetRequest.URL +"]Download link")
        pages.ShowPage(objectViewPage)
        app.SetFocus(objectText)
      }

      if event.Rune() == '/' {
        searchField.SetText(prefix)
        pages.ShowPage(searchModalPage)
        app.SetFocus(searchField)
      }
      return event
    })

    mainView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
      if event.Rune() == 'q' {
        app.Stop()
      }
      return event
    })

    searchField.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
      switch key := event.Key(); key {
      case tcell.KeyEnter :
        // search...
        prefix = searchField.GetText()
        pages.ShowPage(mainViewPage)
        pages.HidePage(searchModalPage)
        // and refresh
        listBucketContent(bucketsView.GetSelection())
      case tcell.KeyEscape :
        prefix = ""
        pages.ShowPage(mainViewPage)
        pages.HidePage(searchModalPage)
        contentView.SetSelectable(true, false)
        contentView.ScrollToBeginning()
        app.SetFocus(contentView)
      }
      return event
    })

    objectText.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
      switch key := event.Key(); key {
      case tcell.KeyEscape :
        prefix = ""
        pages.ShowPage(mainViewPage)
        pages.HidePage(objectViewPage)
        contentView.SetSelectable(true, false)
        contentView.ScrollToBeginning()
        app.SetFocus(contentView)
      }
      if event.Rune() == 'q' {
        app.Stop()
      }
      return event
    })

    // main view layout
    mainView.SetBorders(true).SetColumns(10, 0, 40).SetRows(1, 0, 1)

    // add the widgets
    mainView.
      AddItem(cwdInfo, 0, 0, 1, 3, 1, 1, false).
      AddItem(credentialsView, 1, 0, 1, 1, 1, 1, false).
      AddItem(bucketsView, 1, 1, 1, 1, 1, 1, false).
      AddItem(contentView, 1, 2, 1, 1, 1, 1, false).
      AddItem(fileInfo, 2, 0, 1, 3, 1, 1, true)

    // now the search page
    searchModal := tview.NewGrid().
      SetBorders(true).
      SetColumns(0, 80, 0).
      SetRows(0, 1, 0).
      AddItem(searchField, 1, 1, 1, 1, 0, 0, true)

    // and the main object view page
    objectView := tview.NewGrid().
      SetBorders(true).
      SetColumns(0, 80, 0).
      SetRows(0, 20, 0).
      AddItem(objectText, 1, 1, 1, 1, 0, 0, true)

    // pages setup, for detail view
    pages.AddPage(mainViewPage, mainView, true, true).
      AddPage(searchModalPage, searchModal, true, false).
      AddPage(objectViewPage, objectView, true, false)

    // set root widget
    app.SetRoot(pages, true)

    // select
    app.SetFocus(credentialsView)

    // run
    err := app.Run()
    if err != nil {
      panic(err)
    }
  },
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
  cobra.OnInitialize(loadCredentials)
}


// Load the credentials from ~/.aws/credentials
// toml file
func loadCredentials() {
  homedir, err := os.UserHomeDir()
  creds, err := ini.Load(homedir + "/.aws/credentials")
  if err != nil {
    panic(fmt.Errorf("fatal error credential file: %w", err))
  }
  for _, credential := range creds.Sections() {
    if strings.EqualFold(credential.Name(), "default") {
      continue
    }
    credentials = append(credentials, credential)
  }
}
