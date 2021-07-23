package main

import (
	"bytes"
	"encoding/base64"
	"flag"
	"fmt"
	"hash/maphash"
	"html/template"
	"log"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/gobs/httpclient"
	"github.com/gobs/simplejson"
)

// https://{region}.craigslist.org/search[/area]/{category}?query={}&sort={}&hasPic=1&srchType=T&postedToday=1&bundleDuplicates=1&seach_distance={}&postal={}&min_price={}&max_price={}&crypto_currency=1&delivery_available=1

type ClClient struct {
	h *httpclient.HttpClient
}

func New(region Region) *ClClient {
	uri := fmt.Sprintf(searchuri, region)
	return &ClClient{
		h: httpclient.NewHttpClient(uri),
	}
}

type Region string
type SubRegion string
type SortType string
type Category string

const (
	searchuri = "https://%v.craigslist.org/search/"

	SFBay        = Region("sfbay")
	SanFrancisco = SubRegion("sfa")
	SoutBay      = SubRegion("sby")
	EastBay      = SubRegion("eby")
	NortBay      = SubRegion("nby")
	Peninsula    = SubRegion("pen")
	SantCruz     = SubRegion("scz")

	PriceAsc  = SortType("priceasc")
	PriceDesc = SortType("pricedsc")
	Date      = SortType("date")
	Relevance = SortType("rel")

	ForSale     = Category("sss")
	Bikes       = Category("bia")
	Boats       = Category("boo")
	Cars        = Category("cta")
	Cellphones  = Category("moa")
	Computers   = Category("sya")
	Electronics = Category("ela")
	Free        = Category("zip")
	Music       = Category("msa")
	RVs         = Category("rva")
	Sporting    = Category("sga")
	Tools       = Category("tla")

	pageTemplate = `<!DOCTYPE html>
<html>
  <head>
    <meta charset="UTF-8">
    <title>{{ .Query }}</title>

    <style>

    .row {
      padding: 4px;
    }

    /* Clear floats after the columns */
    .row:after {
      content: "";
      display: table;
      clear: both;
    }

    .column {
      padding: 4px;
    }

    .left {
      float: left;
      width: 300px;
    }

    .right {
        width: 100%;
    }

    </style>
  <head>
  <body>
    <h2>{{ .Query }}</h2>

    {{ range .Entries }}
    <hr/>

    <div class="row">
      <div class="column left">
        <img src="{{ .Image }}">
      </div>

      <div class="column right">
        <a href="{{ .Href }}"><h3>{{ .Title }}</h3></a>
        Added: {{ .Datetime }}<br/>
        Price: {{ .Price }}<br/>
        {{ or .NearbyDesc .Neighborhood }}
      </div>
    </div>
    {{ else }}
    No results
    {{ end }}

  </body>
</html>
`
)

var hashSeed = maphash.MakeSeed()

type ResultEntry struct {
	Title        string
	Href         string
	Image        string
	Datetime     string
	Neighborhood string
	NearbyLoc    string
	NearbyDesc   string
	Price        string
}

func normalize(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "")
	return s
}

func (entry ResultEntry) Hash() uint64 {
	var h maphash.Hash

	h.SetSeed(hashSeed) // we need all hashes for the same string to be the same

	h.WriteString(normalize(entry.Title))
	h.WriteString(normalize(entry.Image))
	h.WriteString(normalize(entry.NearbyLoc))
	h.WriteString(normalize(entry.NearbyDesc))
	h.WriteString(normalize(entry.Neighborhood))
	h.WriteString(normalize(entry.Price))
	return h.Sum64()
}

type SearchResults struct {
	Query   string
	Entries []ResultEntry
	Prev    string
	Next    string
}

type SearchOption func(params map[string]interface{})

func WithRegion(r Region) SearchOption {
	return func(params map[string]interface{}) {
		if string(r) != "" {
			params["region"] = string(r)
		}
	}
}

func WithSubregion(r SubRegion) SearchOption {
	return func(params map[string]interface{}) {
		if string(r) != "" {
			params["subregion"] = string(r)
		}
	}
}

func WithCategory(c Category) SearchOption {
	return func(params map[string]interface{}) {
		if string(c) != "" {
			params["category"] = string(c)
		}
	}
}

func Query(q string) SearchOption {
	return func(params map[string]interface{}) {
		params["query"] = q
	}
}

func Sort(s SortType) SearchOption {
	return func(params map[string]interface{}) {
		if string(s) != "" {
			params["sort"] = string(s)
		}
	}
}

func Pictures(pictures bool) SearchOption {
	return func(params map[string]interface{}) {
		if pictures {
			params["hasPic"] = 1
		}
	}
}

func Today(today bool) SearchOption {
	return func(params map[string]interface{}) {
		if today {
			params["postedToday"] = 1
		}
	}
}

func Dedup(dedup bool) SearchOption {
	return func(params map[string]interface{}) {
		if dedup {
			params["bundleDuplicates"] = 1
		}
	}
}

func TitleOnly(only bool) SearchOption {
	return func(params map[string]interface{}) {
		if only {
			params["srchType"] = "T"
		}
	}
}

func SearchDistance(d int) SearchOption {
	return func(params map[string]interface{}) {
		params["search_distance"] = d
	}
}

func PostalCode(p string) SearchOption {
	return func(params map[string]interface{}) {
		params["postal_code"] = p
	}
}

func MinPrice(p int) SearchOption {
	return func(params map[string]interface{}) {
		if p > 0 {
			params["min_price"] = p
		}
	}
}

func MaxPrice(p int) SearchOption {
	return func(params map[string]interface{}) {
		if p > 0 {
			params["max_price"] = p
		}
	}
}

func (c *ClClient) Search(options ...SearchOption) (*SearchResults, error) {
	params := map[string]interface{}{}

	for _, opt := range options {
		opt(params)
	}

	reqs := []httpclient.RequestOption{}

	if r, ok := params["region"]; ok {
		uri := fmt.Sprintf(searchuri, r.(string))
		delete(params, "region")
		reqs = append(reqs, httpclient.URLString(uri))
	}

	path := ""

	if r, ok := params["subregion"]; ok {
		path = r.(string) + "/"
		delete(params, "subregion")
	}

	if c, ok := params["category"]; ok {
		path += c.(string)
		delete(params, "category")
	} else {
		path += string(ForSale)
	}

	reqs = append(reqs, httpclient.Path(path))
	reqs = append(reqs, httpclient.Params(params))
	res, err := httpclient.CheckStatus(c.h.SendRequest(reqs...))
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(res.Body)
	res.Body.Close()

	if err != nil {
		return nil, err
	}

	var results SearchResults

	if q, ok := params["query"]; ok {
		results.Query = q.(string)
	} else {
		results.Query = "Results"
	}

	dedup := params["bundleDuplicates"] != nil
	duplicates := map[uint64]bool{}

	doc.Find(".rows li.result-row").Each(func(i int, s *goquery.Selection) {
		title := s.Find(".result-heading a").First().Text()
		href, _ := s.Find(".result-heading a").First().Attr("href")
		iids, _ := s.Find("a.result-image").Attr("data-ids")
		datetime, _ := s.Find(".result-info .result-date").First().Attr("datetime")
		hood := s.Find(".result-meta .result-hood").First().Text()
		nearby := s.Find(".result-meta .nearby").First()
		loc, _ := nearby.Attr("title")
		ldesc := nearby.Text()
		price := s.Find(".result-meta .result-price").First().Text()

		image := ""
		ids := strings.Split(iids, ",")
		if len(ids) > 0 {
			parts := strings.Split(ids[0], ":")
			image = fmt.Sprintf("https://images.craigslist.org/%v_300x300.jpg", parts[1])
		}

		entry := ResultEntry{
			Title:        title,
			Href:         href,
			Image:        image,
			Datetime:     datetime,
			NearbyLoc:    loc,
			NearbyDesc:   strings.TrimSpace(ldesc),
			Neighborhood: strings.TrimSpace(hood),
			Price:        price,
		}

		if dedup {
			h := entry.Hash()
			if duplicates[h] == true {
				//log.Println("hash", h, "duplicate", entry)
				return
			}

			duplicates[h] = true
			//log.Println("duplicates", duplicates)
		}

		results.Entries = append(results.Entries, entry)

		//fmt.Println("<!-------------------------------------------------------------------------------->")
		//fmt.Println(goquery.OuterHtml(s))
		//fmt.Println("<!-------------------------------------------------------------------------------->")
	})

	results.Prev, _ = doc.Find(".buttons .prev").Attr("href")
	results.Next, _ = doc.Find(".buttons .next").Attr("href")

	return &results, nil
}

func mapCategory(name string) Category {
	var categories = map[string]Category{
		"all":         ForSale,
		"bikes":       Bikes,
		"boats":       Boats,
		"cars":        Cars,
		"phones":      Cellphones,
		"computers":   Computers,
		"electronics": Electronics,
		"free":        Free,
		"music":       Music,
		"rvs":         RVs,
		"sports":      Sporting,
		"tools":       Tools,
	}

	if c, ok := categories[name]; ok {
		return c
	}

	return Category(name)
}

func applyFilter(f string, in []ResultEntry) (out []ResultEntry) {
	if f == "" {
		return in
	}

	f = strings.ToLower(f)

	var neg bool
	var any bool
	var fpat []string

	if strings.HasPrefix(f, "^") {
		neg = true
		f = f[1:]
	}

	if strings.Contains(f, "|") { // any
		any = true
		fpat = strings.Split(f, "|")
	} else if strings.ContainsAny(f, "&, ") { // all
		fpat = regexp.MustCompile("[& ,]").Split(f, -1)
	} else { // one element
		any = true
		fpat = []string{f}
	}

	out = make([]ResultEntry, 0, len(in))
	re := regexp.MustCompile("(" + strings.Join(fpat, "|") + ")")

	for _, r := range in {
		res := re.FindAllString(strings.ToLower(r.Title), -1)
		if len(res) == 0 {
			if neg {
				out = append(out, r)
			}

			continue
		}

		if any { // any match count
			if !neg {
				out = append(out, r)
			}
			continue
		}

		// check that all pattern matched
		matches := map[string]bool{}

		for _, v := range res {
			matches[v] = true
		}

		match := len(matches) == len(fpat)
		if neg {
			match = !match
		}

		if match {
			out = append(out, r)
		}
	}

	return
}

func openbrowser(url string) {
	var err error

	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}
	if err != nil {
		log.Fatal(err)
	}

}

func main() {
	region := flag.String("region", "sfbay", "Region")
	subregion := flag.String("subregion", "", "Subregion")
	cat := flag.String("cat", "sss", "Category")
	dedup := flag.Bool("dedup", true, "Bundle duplicates")
	pictures := flag.Bool("pictures", true, "Has pictures")
	sort := flag.String("sort", "", "Sort type (priceasc,pricedsc,date,rel")
	titleOnly := flag.Bool("titles", false, "Search in title only")
	filter := flag.String("filter", "", "Title filter")
	today := flag.Bool("today", false, "Added today")
	min := flag.Int("min", 0, "Min price")
	max := flag.Int("max", 0, "Max price")
	html := flag.Bool("html", false, "Return an HTML page")
	browse := flag.Bool("browse", false, "Create HTML page and open browser")
	flag.Parse()

	query := strings.Join(flag.Args(), " ")

	cl := New(Region(*region))
	res, err := cl.Search(
		WithSubregion(SubRegion(*subregion)),
		WithCategory(mapCategory(*cat)),
		Dedup(*dedup),
		Pictures(*pictures),
		Sort(SortType(*sort)),
		TitleOnly(*titleOnly),
		Today(*today),
		MinPrice(*min),
		MaxPrice(*max),
		Query(query))

	if err != nil {
		fmt.Println("ERROR", err)
		return
	}

	if *filter != "" {
		res.Query = fmt.Sprintf("%v (filter: %v)", res.Query, *filter)
		res.Entries = applyFilter(*filter, res.Entries)

	}

	if *browse {
		var b bytes.Buffer
		t := template.Must(template.New("webpage").Parse(pageTemplate))
		t.Execute(&b, res)

		// note that by default data: URLs don't "open" in MacOS
		// and you need to add a mapping scheme -> app
		// (see for example SwiftDefaultApps)
		durl := fmt.Sprintf("data:text/html;base64,%v", base64.StdEncoding.EncodeToString(b.Bytes()))
		openbrowser(durl)
	} else if *html {
		t := template.Must(template.New("webpage").Parse(pageTemplate))
		t.Execute(os.Stdout, res)
	} else {
		fmt.Println(simplejson.MustDumpString(res, simplejson.Indent(" ")))
	}
}