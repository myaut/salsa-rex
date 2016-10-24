package salsacore

import (
	"fmt"
	"io"
	"bytes"
	
	"flag"
	
	"encoding/json"
	"net/http"
	
	"github.com/go-ini/ini"
)

type Client struct {
	// Base connection string for server
	server string
	
	// Response returned from server
	resp *http.Response
}

func NewClient(server string) (*Client) {
	return &Client{server: server}
}

// encodes value to an in-memory pipe and return reader's 
// endpoint for this pipe
func encodeJSON(value interface{}) (io.Reader) {
	buffer := bytes.NewBuffer([]byte{})
	encoder := json.NewEncoder(buffer)
	
	encoder.Encode(value)
	
	return buffer
}

func (c *Client) Get(path string) (err error) {
	c.resp, err = http.Get(c.server + path)
	return
}

func (c *Client) GetValue(path string, value interface{}) (err error) {
	c.resp, err = http.Get(c.server + path)
	if err != nil {
		return
	}
	
	return c.DecodeResponse(value)
}

func (c *Client) PostValue(path string, value interface{}) (err error) {
	c.resp, err = http.Post(c.server + path, "application/json", encodeJSON(value))
	return
}

// Parses arguments left by flag interface into an "ini" file
// and creates a request based on that arguments. Useful for CLI 
// tools
func (c *Client) PostArguments(path string, value interface{}) (error) {
	buffer := bytes.NewBuffer([]byte{})
	
	for _, arg := range flag.Args() {
		buffer.WriteString(arg)
		buffer.WriteRune('\n')
	}
	
	cfg, err := ini.Load(buffer.Bytes())
	if err != nil {
		return err
	}
	
	err = cfg.MapTo(value)
	if err != nil {
		return err
	}
	
	return c.PostValue(path, value)
}

func (c *Client) DecodeResponse(value interface{}) (error) {
	if c.resp == nil || c.resp.Body == nil {
		return fmt.Errorf("No response received")
	}
	
	if c.resp.StatusCode >= 400 {
		return fmt.Errorf("Error in %s %s: %s", c.resp.Request.Method, 
					c.resp.Request.URL.Path, c.resp.Status)
	}
	
	return json.NewDecoder(c.resp.Body).Decode(value)
}

func (c *Client) DecodeObjectKey() (key string, err error) {
	err = c.DecodeResponse(&key)
	return
}

func (c* Client) CopyResponse(w io.Writer) {
	if c.resp == nil || c.resp.Body == nil {
		return
	}
	
	io.Copy(w, c.resp.Body)
}
