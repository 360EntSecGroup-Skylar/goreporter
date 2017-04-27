package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	// "github.com/wgliang/goreporter/linters/aligncheck"
	"github.com/wgliang/goreporter/linters/copycheck"
	"github.com/wgliang/goreporter/linters/cyclo"
	"github.com/wgliang/goreporter/linters/deadcode"
	"github.com/wgliang/goreporter/linters/depend"
	// "github.com/wgliang/goreporter/linters/errorcheck"
	"github.com/wgliang/goreporter/linters/simplecode"
	"github.com/wgliang/goreporter/linters/staticscan"
	// "github.com/wgliang/goreporter/linters/structcheck"
	"github.com/wgliang/goreporter/linters/spellcheck"
	"github.com/wgliang/goreporter/linters/unittest"
	// "github.com/wgliang/goreporter/linters/varcheck"
)

var (
	tpl string
)

type WaitGroupWrapper struct {
	sync.WaitGroup
}

func (w *WaitGroupWrapper) Wrap (cb func()){
	w.Add(1)
	go func() {
		cb()
		w.Done()
	}()
}

func NewReporter() *Reporter {
	return &Reporter{}
}

func (r *Reporter) Engine(projectPath string, exceptPackages string) {

	log.Println("start code quality assessment...")
	wg := &WaitGroupWrapper{}

	dirsUnitTest, err := DirList(projectPath, "_test.go", exceptPackages)
	if err != nil {
		log.Println(err)
	}
	r.Project = projectName(projectPath)
	var importPkgs []string

	unitTestF := func() {
		log.Println("running unit test...")
		packagesTestDetail := struct {
			Values map[string]PackageTest
			mux    *sync.RWMutex
		}{make(map[string]PackageTest, 0), new(sync.RWMutex)}
		packagesRaceDetail := struct {
			Values map[string][]string
			mux    *sync.RWMutex
		}{make(map[string][]string, 0), new(sync.RWMutex)}

		sumCover := 0.0
		countCover := 0
		var pkg sync.WaitGroup
		for pkgName, pkgPath := range dirsUnitTest {
			pkg.Add(1)
			go func(pkgName, pkgPath string) {
				unitTestRes, unitRaceRes := unittest.UnitTest("./" + pkgPath)
				var packageTest PackageTest
				if len(unitTestRes) >= 1 {
					testres := unitTestRes[pkgName]
					if len(testres) > 5 {
						if testres[0] == "ok" {
							packageTest.IsPass = true
						} else {
							packageTest.IsPass = false
						}
						timeLen := len(testres[2])
						if timeLen > 1 {
							time, err := strconv.ParseFloat(testres[2][:(timeLen-1)], 64)
							if err == nil {
								packageTest.Time = time
							} else {
								log.Println(err)
							}
						}
						packageTest.Coverage = testres[4]

						coverLen := len(testres[4])
						if coverLen > 1 {
							coverFloat, _ := strconv.ParseFloat(testres[4][:(coverLen-1)], 64)
							sumCover = sumCover + coverFloat
							countCover = countCover + 1
						} else {
							countCover = countCover + 1
						}
					} else {
						packageTest.Coverage = "0%"
						countCover = countCover + 1
					}
				} else {
					packageTest.Coverage = "0%"
					countCover = countCover + 1
				}
				packagesTestDetail.mux.Lock()
				packagesTestDetail.Values[pkgName] = packageTest
				packagesTestDetail.mux.Unlock()

				if len(unitRaceRes[pkgName]) > 0 {
					packagesRaceDetail.mux.Lock()
					packagesRaceDetail.Values[pkgName] = unitRaceRes[pkgName]
					packagesRaceDetail.mux.Unlock()
				}
				pkg.Done()
			}(pkgName, pkgPath)
		}

		pkg.Wait()
		packagesTestDetail.mux.Lock()
		r.UnitTestx.PackagesTestDetail = packagesTestDetail.Values
		packagesTestDetail.mux.Unlock()
		r.UnitTestx.AvgCover = fmt.Sprintf("%.1f", sumCover/float64(countCover)) + "%"
		packagesRaceDetail.mux.Lock()
		r.UnitTestx.PackagesRaceDetail = packagesRaceDetail.Values
		packagesRaceDetail.mux.Unlock()

		log.Println("unit test over!")
	}

	cycloF := func() {
		log.Println("computing cyclo...")

		dirsAll, err := DirList(projectPath, ".go", exceptPackages)
		if err != nil {
			log.Println(err)
		}

		cycloRes := make(map[string]Cycloi, 0)
		for pkgName, pkgPath := range dirsAll {
			cyclo, avg := cyclo.Cyclo(pkgPath)
			cycloRes[pkgName] = Cycloi{
				Average: avg,
				Result:  cyclo,
			}
		}
		r.Cyclox = cycloRes
		log.Println("cyclo over!")
	}

	simpleCodeF := func() {
		log.Println("simpling code...")

		simples := simplecode.SimpleCode(projectPath)
		simpleTips := make(map[string][]string, 0)
		for _, tips := range simples {
			index := strings.Index(tips, ":")
			simpleTips[PackageAbsPathExceptSuffix(tips[0:index])] = append(simpleTips[PackageAbsPathExceptSuffix(tips[0:index])], tips)
		}
		r.SimpleTips = simpleTips
		log.Println("simpled code!")

	}

	copyCheckF := func() {
		log.Println("checking copy code...")

		x := copycheck.CopyCheck(projectPath, "_test.go")
		r.CopyTips = x
		log.Println("checked copy code!")
	}

	scanTipsF := func() {
		log.Println("running staticscan...")

		staticscan.StaticScan(projectPath)
		scanTips := make(map[string][]string, 0)
		tips := staticscan.StaticScan(projectPath)
		for _, tip := range tips {
			index := strings.Index(tip, ":")
			scanTips[PackageAbsPathExceptSuffix(tip[0:index])] = append(scanTips[PackageAbsPathExceptSuffix(tip[0:index])], tip)
		}
		r.ScanTips = scanTips
		log.Println("staticscan over!")
	}

	dependGraphF := func() {
		log.Println("creating depend graph...")
		r.DependGraph = depend.Depend(projectPath, exceptPackages)
		log.Println("created depend graph")
	}

	deadCodeF := func() {
		log.Println("checking dead code...")
		r.DeadCode = deadcode.DeadCode(projectPath)
		log.Println("checked dead code")
	}

	spellCheckF := func() {
		log.Println("checking spell error...")
		r.SpellError = spellcheck.SpellCheck(projectPath, exceptPackages)
		log.Println("checked spell error")
	}

	importPkgsf := func() {
		log.Println("getting import packages...")
		importPkgs = unittest.GoListWithImportPackages(projectPath)
		log.Println("import packages done")
	}

	wg.Wrap(unitTestF)
	wg.Wrap(cycloF)
	wg.Wrap(simpleCodeF)
	wg.Wrap(copyCheckF)
	wg.Wrap(scanTipsF)
	wg.Wrap(dependGraphF)
	wg.Wrap(deadCodeF)
	wg.Wrap(spellCheckF)
	wg.Wrap(importPkgsf)

	wg.Wait()

	// get all no unit test packages
	noTestPackage := make([]string, 0)
	for i := 0; i < len(importPkgs); i++ {
		if _, ok := r.UnitTestx.PackagesTestDetail[importPkgs[i]]; !ok {
			noTestPackage = append(noTestPackage, importPkgs[i])
		}
	}
	r.NoTestPkg = noTestPackage

	log.Println("finished code quality assessment...")
}

func (r *Reporter) formateReport2Json() []byte {
	report, err := json.Marshal(r)
	if err != nil {
		log.Println("json err:", err)
	}

	return report
}

func (r *Reporter) SaveAsHtml(htmlData HtmlData, projectPath, savePath, timestamp string) {
	if tpl == "" {
		tpl = defaultTpl
	}

	t, err := template.New("goreporter").Parse(tpl)
	if err != nil {
		log.Println(err)
	}

	var out bytes.Buffer
	err = t.Execute(&out, htmlData)
	if err != nil {
		log.Println(err)
	}
	projectName := projectName(projectPath)
	if savePath != "" {
		htmlpath := strings.Replace(savePath+string(filepath.Separator)+projectName+"-"+timestamp+".html", string(filepath.Separator)+string(filepath.Separator), string(filepath.Separator), -1)
		log.Println(htmlpath)
		err = ioutil.WriteFile(htmlpath, out.Bytes(), 0666)
		if err != nil {
			log.Println(err)
		}
	} else {
		htmlpath := projectName + "-" + timestamp + ".html"
		log.Println(htmlpath)
		err = ioutil.WriteFile(htmlpath, out.Bytes(), 0666)
		if err != nil {
			log.Println(err)
		}
	}
}

func (r *Reporter) Grade() int {
	score := 0.0
	tscore := float64(40)
	if len(r.UnitTestx.AvgCover) > 1 {
		cover, err := strconv.ParseFloat(r.UnitTestx.AvgCover[:(len(r.UnitTestx.AvgCover)-1)], 64)
		if err != nil {
			cover = 0
		}
		score = score + tscore*cover/100.0
	}

	countCopy := len(r.CopyTips)
	if countCopy < 10 {
		score = score + float64(10-1*countCopy)
	}

	countScan := 0
	for _, pkg := range r.ScanTips {
		countScan = countScan + len(pkg)
	}
	if countScan < 10 {
		score = score + float64(10-1*countScan)
	}

	countSimple := 0
	for _, pkg := range r.SimpleTips {
		countSimple = countSimple + len(pkg)
	}
	if countSimple < 10 {
		score = score + float64(10-1*countSimple)
	}

	sscore := 10.0
	sscore = sscore - float64(len(r.DeadCode)/5)
	if sscore < 0 {
		sscore = 0
	}
	score = score + sscore

	sum15 := 0
	sum50 := 0
	countcyclo := 0
	sum := 0
	pscore := 0
	for _, val := range r.Cyclox {
		for _, v := range val.Result {
			var num int
			in := strings.Index(v, " ")
			if in > 0 {
				countcyclo++
				num, _ = strconv.Atoi(v[0:in])
				if num >= 50 {
					sum50++
					sum15++
				} else if num >= 15 {
					sum15++
				} else {
					sum += num
				}
			}
		}
	}

	if (countcyclo - sum50 - sum15) > 0 {
		pscore = 20 * ((15 * 1.0 * (countcyclo - sum50 - sum15)) - sum) / (15 * (countcyclo - sum50 - sum15))
	} else {
		pscore = 0
	}

	pscore = pscore - sum50/5 - sum15/10
	if pscore < 0 {
		pscore = 0
	}
	score = score + float64(pscore)
	r.Score = int(score)
	return int(score)
}

func (r *Reporter) SaveAsJson(projectPath, savePath, timestamp string) {
	jsonData := r.formateReport2Json()
	savePath = absPath(savePath)
	projectName := projectName(projectPath)
	if savePath != "" {
		jsonpath := strings.Replace(savePath+string(filepath.Separator)+projectName+"-"+timestamp+".json", string(filepath.Separator)+string(filepath.Separator), string(filepath.Separator), -1)
		err := ioutil.WriteFile(jsonpath, jsonData, 0666)
		if err != nil {
			log.Println(err)
		}
	} else {
		jsonpath := projectName + "-" + timestamp + ".json"
		err := ioutil.WriteFile(jsonpath, jsonData, 0666)
		if err != nil {
			log.Println(err)
		}
	}
}
