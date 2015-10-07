/*
 * This is an unpublished work copyright 2015 Jens-Uwe Mager
 * 30177 Hannover, Germany, jum@anubis.han.de
 */

//A command line tool to collect spot messenger messages via the
//public feed and optionally format the collected messages as a GPX
//file. As the public feed does only keep the messages for about
//seven days running this utility regularly via cron will keep a
//combined database of all spot messages.
package main

import (
	"bufio"
	"encoding/gob"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"net/url"
	"os"
	"sort"
	"time"

	"github.com/jum/spot"
)

var (
	messageDB *string = flag.String("messages", "messages.db", "Name of message database file")
	feedID    *string = flag.String("feedid", "", "Spot Messenger feed ID")
	verbose   *bool   = flag.Bool("verbose", false, "Be more verbose about what is happening")
	quiet     *bool   = flag.Bool("quiet", false, "Be quiet about temporary and network errors")
	printgpx  *string = flag.String("printgpx", "", "Name of file to print messages in GPX format to")
)

func main() {
	var messages []spot.Message
	flag.Parse()
	modified := false
	//
	// Open the old message DB and read old messages
	//
	f, err := os.Open(*messageDB)
	if err == nil {
		defer f.Close()
		bf := bufio.NewReader(f)
		dec := json.NewDecoder(bf)
		err = dec.Decode(&messages)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning %v:%v\n", *messageDB, err)
			_, err = f.Seek(0, os.SEEK_SET)
			if err != nil {
				panic(err)
			}
			bf = bufio.NewReader(f)
			dec := gob.NewDecoder(bf)
			err = dec.Decode(&messages)
			if err != nil {
				panic(err)
			}
			// force saving in json format
			modified = true
		}
	} else {
		fmt.Fprintf(os.Stderr, "Warning %v:%v\n", *messageDB, err)
	}
	//
	// For compatibility, read and merge any JSON files mentioned
	// on the command line, these where saved via curl.
	//
	for _, fname := range flag.Args() {
		if *verbose {
			fmt.Printf("fname = %v\n", fname)
		}
		f, err := os.Open(fname)
		if err != nil {
			panic(err)
		}
		d, err := spot.DecodeFeed(f)
		if err != nil {
			panic(err)
		}
		//fmt.Printf("d = %#v\n", d)
		f.Close()
		num := len(messages)
		messages = spot.MergeMessages(messages, d.Response.FeedMessageResponse.Messages.Message)
		if len(messages) > num {
			modified = true
			if *verbose {
				fmt.Printf("Added %v messages via %v\n", len(messages)-num, fname)
			}
		}
	}
	//
	// Read and merge any messages available on the shared
	// Spot Messenger feed.
	//
	if len(*feedID) > 0 {
		n, err := spot.RetrieveMessages(*feedID)
		if err != nil {
			if *quiet {
				//fmt.Printf("err = %#v\n", err)
				if e, ok := err.(spot.Error); ok {
					if e.Code == "E-0195" {
						err = nil
					}
				}
				testErr := err
				if e, ok := testErr.(*url.Error); ok {
					testErr = e.Err
				}
				// As we are called regularly by cron, ignore some errors.
				type timeout interface {
					Timeout() bool
				}
				if e, ok := testErr.(timeout); ok {
					if e.Timeout() {
						err = nil
					}
				}
				type temporary interface {
					Temporary() bool
				}
				if e, ok := testErr.(temporary); ok {
					if e.Temporary() {
						err = nil
					}
				}
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %s\n", os.Args[0], err)
				os.Exit(1)
			}
		}
		//fmt.Printf("n = %#v\n", n)
		num := len(messages)
		messages = spot.MergeMessages(messages, n)
		if len(messages) > num {
			modified = true
			if *verbose {
				fmt.Printf("Added %v messages via feed %v\n", len(messages)-num, *feedID)
			}
		}
	}
	//
	// Sort the database in time order, this is easier to read.
	//
	sort.Sort(spot.MessageTimeSorter(messages))
	//
	// Save the new messages to the DB
	//
	if modified {
		j, err := json.MarshalIndent(messages, "", "  ")
		if err != nil {
			panic(err)
		}
		tmpName := *messageDB + ".tmp"
		f, err = os.Create(tmpName)
		if err != nil {
			panic(err)
		}
		_, err = f.Write(j)
		if err != nil {
			panic(err)
		}
		err = f.Close()
		if err != nil {
			panic(err)
		}
		err = os.Rename(tmpName, *messageDB)
		if err != nil {
			panic(err)
		}
	}
	if len(*printgpx) > 0 {
		f, err := os.Create(*printgpx)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		bf := bufio.NewWriter(f)
		defer bf.Flush()
		_, err = bf.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<gpx
 version="1.1"
 creator="spot.go"
 xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
 xmlns="http://www.topografix.com/GPX/1/1"
 xsi:schemaLocation="http://www.topografix.com/GPX/1/1 http://www.topografix.com/GPX/1/1/gpx.xsd">
`))
		if err != nil {
			panic(err)
		}
		//<wpt lat="54.093333333333334" lon="10.806"><name>WPT000</name><time>2007-02-21T15:33:00Z</time><cmt>Test vom Land aus</cmt></wpt>
		for _, e := range messages {
			if len(e.MessageContent) > 0 {
				fmt.Fprintf(bf, "<wpt lat=\"%f\" lon=\"%f\"><name>%v</name><time>%s</time><cmt>%s</cmt></wpt>\n", e.Latitude, e.Longitude, e.Id, time.Unix(e.UnixTime, 0).UTC().Format(time.RFC3339), html.EscapeString(e.MessageContent))
			} else {
				fmt.Fprintf(bf, "<wpt lat=\"%f\" lon=\"%f\"><name>%v</name><time>%s</time></wpt>\n", e.Latitude, e.Longitude, e.Id, time.Unix(e.UnixTime, 0).UTC().Format(time.RFC3339))
			}
		}
		_, err = bf.Write([]byte(`</gpx>
`))
		if err != nil {
			panic(err)
		}
	}
}
