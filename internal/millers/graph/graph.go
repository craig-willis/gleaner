package graph

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"strings"
	"sync"

	"earthcube.org/Project418/gleaner/internal/common"
	"earthcube.org/Project418/gleaner/internal/millers/millerutils"
	"earthcube.org/Project418/gleaner/pkg/utils"

	"github.com/knakk/rdf"
	minio "github.com/minio/minio-go"
)

// GraphMillObjects test a concurrent version of calling mock
func GraphMillObjects(mc *minio.Client, bucketname string, cs utils.Config) {
	entries := common.GetMillObjects(mc, bucketname)
	multiCall(entries, bucketname, mc, cs)
}

func multiCall(e []common.Entry, bucketname string, mc *minio.Client, cs utils.Config) {
	// Set up the the semaphore and conccurancey
	semaphoreChan := make(chan struct{}, 20) // a blocking channel to keep concurrency under control
	defer close(semaphoreChan)
	wg := sync.WaitGroup{} // a wait group enables the main process a wait for goroutines to finish

	var gb common.Buffer

	for k := range e {
		wg.Add(1)
		log.Printf("About to run #%d in a goroutine\n", k)
		go func(k int) {
			semaphoreChan <- struct{}{}
			status := millerutils.Jsl2graph(e[k].Bucketname, e[k].Key, e[k].Urlval, e[k].Sha1val, e[k].Jld, &gb)

			wg.Done() // tell the wait group that we be done!
			log.Printf("#%d wrote %d bytes", k, status)
			<-semaphoreChan
		}(k)
	}
	wg.Wait()

	log.Println(gb.Len())

	// STEP 1 clean triples  (split to two buffers..   good and bad)
	// STEP 1 covert good buffer NT to NQ (so I need the context from the config file to define the graph)
	var err error
	scanner := bufio.NewScanner(&gb) // rdf is already a pointer
	good := bytes.NewBuffer(make([]byte, 0))
	bad := bytes.NewBuffer(make([]byte, 0))
	for scanner.Scan() {
		if len(scanner.Text()) > 2 {
			nq, e := goodTriples(scanner.Text(), fmt.Sprintf("http://earthcube.org/%s", bucketname))
			if e == nil {
				_, err = good.Write([]byte(nq))
			}
			if e != nil {
				_, err = bad.Write([]byte(fmt.Sprintf("%s :Error: %s\n", strings.TrimSuffix(scanner.Text(), "\n"), e)))
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}

	// TODO  write both of these to the Minio system
	log.Println(good.Len())
	log.Println(bad.Len())

	// TODO: Can we clear up gb at this point if we use these good/bad buffers from here out?

	// write two object to S3; the quads and the error list
	flgood, err := millerutils.LoadToMinio(good.String(), "gleaner-milled", fmt.Sprintf("%s/%s.nq", cs.Gleaner.RunID, bucketname), mc)
	if err != nil {
		log.Println("RDF file could not be written")
	} else {
		log.Printf("RDF file written len:%d\n", flgood)
	}
	if bad.Len() > 0 { // when the light is green, the trap is clean
		flbad, err := millerutils.LoadToMinio(bad.String(), "gleaner-milled", fmt.Sprintf("%s/%s_rdfErrors.txt", cs.Gleaner.RunID, bucketname), mc)
		if err != nil {
			log.Println("RDF Error file could not be written")
		} else {
			log.Printf("RDF Error file written len:%d\n", flbad)
		}
	}
}

// TODO  convert this to use a bytes.Buffer  (or better a pointer to that)
func goodTriples(f, c string) (string, error) {
	fmt.Printf("Trying: %s \n", f)
	dec := rdf.NewTripleDecoder(strings.NewReader(f), rdf.NTriples)
	triple, err := dec.Decode()
	if err != nil {
		log.Printf("Error decoding triples: %v\n", err)
		return "", err
	}

	// enc := rdf.NewQuadEncoder(outFile, rdf.NQuads)
	q, err := makeQuad(triple, c)
	if err != nil {
		return "", err
	}

	return fmt.Sprint(q), err
}

// makeQuad make a quad from a triple and a context string
func makeQuad(t rdf.Triple, c string) (string, error) {
	newctx, err := rdf.NewIRI(c)
	ctx := rdf.Context(newctx)

	q := rdf.Quad{t, ctx}
	buf := bytes.NewBufferString("")
	qs := q.Serialize(rdf.NQuads)
	fmt.Fprintf(buf, "%s", qs)

	return buf.String(), err
}
