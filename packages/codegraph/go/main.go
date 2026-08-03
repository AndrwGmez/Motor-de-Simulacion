// Analizador de Go para @flowverse/codegraph.
//
// Emite el mismo modelo intermedio que el adaptador de TypeScript, de modo que
// los extractores no necesitan saber de qué lenguaje viene el código. Usa
// go/ast, que es el analizador del propio compilador: nada de expresiones
// regulares adivinando sintaxis.
package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

type modulo struct {
	Ruta       string `json:"ruta"`
	Directorio string `json:"directorio"`
	Nombre     string `json:"nombre"`
	Papel      string `json:"papel"`
	Motivo     string `json:"motivo,omitempty"`
}

type referencia struct {
	Desde string `json:"desde"`
	Hacia string `json:"hacia"`
	Clase string `json:"clase"`
}

type funcion struct {
	Nombre  string `json:"nombre"`
	Archivo string `json:"archivo"`
	Papel   string `json:"papel"`
}

type llamada struct {
	Desde string `json:"desde"`
	Hacia string `json:"hacia"`
}

type salida struct {
	Modulos      []modulo     `json:"modulos"`
	Referencias  []referencia `json:"referencias"`
	Funciones    []funcion    `json:"funciones"`
	Llamadas     []llamada    `json:"llamadas"`
}

var persistencia = []string{"database/sql", "gorm.io", "jackc/pgx", "go-redis", "mongo-driver", "sqlx"}
var servicios = []string{"net/http", "aws-sdk", "grpc", "amqp", "kafka", "sendgrid"}

func contiene(valor string, lista []string) bool {
	for _, item := range lista {
		if strings.Contains(valor, item) {
			return true
		}
	}
	return false
}

func main() {
	if len(os.Args) < 2 {
		os.Stderr.WriteString("uso: analizador <ruta>\n")
		os.Exit(2)
	}
	raiz := os.Args[1]
	resultado := salida{Modulos: []modulo{}, Referencias: []referencia{}, Funciones: []funcion{}, Llamadas: []llamada{}}
	conjunto := token.NewFileSet()
	propias := map[string]bool{}
	pendientes := []llamada{}

	filepath.Walk(raiz, func(ruta string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if nombre := info.Name(); nombre == "vendor" || nombre == ".git" || strings.HasPrefix(nombre, ".") && len(nombre) > 1 {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(ruta, ".go") || strings.HasSuffix(ruta, "_test.go") {
			return nil
		}
		archivo, err := parser.ParseFile(conjunto, ruta, nil, parser.AllErrors)
		if err != nil {
			return nil
		}
		relativa, _ := filepath.Rel(raiz, ruta)
		relativa = filepath.ToSlash(relativa)
		partes := strings.Split(relativa, "/")
		directorio := ""
		if len(partes) > 1 {
			directorio = partes[len(partes)-2]
		}

		papel, motivo := "process", ""
		for _, imp := range archivo.Imports {
			valor := strings.Trim(imp.Path.Value, `"`)
			if contiene(valor, persistencia) {
				papel, motivo = "data", valor
				break
			}
			if contiene(valor, servicios) {
				papel, motivo = "integration", valor
				break
			}
		}
		resultado.Modulos = append(resultado.Modulos, modulo{
			Ruta: relativa, Directorio: directorio, Nombre: partes[len(partes)-1], Papel: papel, Motivo: motivo,
		})

		// Importaciones internas: las que apuntan a un paquete del propio árbol.
		for _, imp := range archivo.Imports {
			valor := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(valor, ".") || strings.Contains(valor, filepath.Base(raiz)+"/") {
				resultado.Referencias = append(resultado.Referencias, referencia{Desde: relativa, Hacia: valor, Clase: "import"})
			}
		}

		for _, declaracion := range archivo.Decls {
			funcDecl, ok := declaracion.(*ast.FuncDecl)
			if !ok || funcDecl.Name == nil {
				continue
			}
			nombre := funcDecl.Name.Name
			propias[nombre] = true
			papelFuncion := "process"
			ast.Inspect(funcDecl, func(n ast.Node) bool {
				if llamadaExpr, ok := n.(*ast.CallExpr); ok {
					if sel, ok := llamadaExpr.Fun.(*ast.SelectorExpr); ok {
						texto := sel.Sel.Name
						if strings.Contains(strings.ToLower(texto), "query") || strings.Contains(strings.ToLower(texto), "exec") {
							papelFuncion = "data"
						}
						if strings.Contains(strings.ToLower(texto), "get") || strings.Contains(strings.ToLower(texto), "post") {
							papelFuncion = "integration"
						}
					}
					if ident, ok := llamadaExpr.Fun.(*ast.Ident); ok {
						pendientes = append(pendientes, llamada{Desde: nombre, Hacia: ident.Name})
					}
				}
				return true
			})
			resultado.Funciones = append(resultado.Funciones, funcion{Nombre: nombre, Archivo: relativa, Papel: papelFuncion})
		}
		return nil
	})

	for _, l := range pendientes {
		if propias[l.Hacia] && l.Desde != l.Hacia {
			resultado.Llamadas = append(resultado.Llamadas, l)
		}
	}

	json.NewEncoder(os.Stdout).Encode(resultado)
}
