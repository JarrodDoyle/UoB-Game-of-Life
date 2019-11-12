package main

import (
	"strconv"
	"strings"
)

func countAliveNeighbours(world [][]byte, x, y, w, h int) int {
	aliveNeighbours := 0
	aliveNeighbours += int(world[(y-1+h)%h][(x-1+w)%w])
	aliveNeighbours += int(world[(y-1+h)%h][x])
	aliveNeighbours += int(world[(y-1+h)%h][(x+1)%w])
	aliveNeighbours += int(world[y][(x-1+w)%w])
	aliveNeighbours += int(world[y][(x+1)%w])
	aliveNeighbours += int(world[(y+1)%h][(x-1+w)%w])
	aliveNeighbours += int(world[(y+1)%h][x])
	aliveNeighbours += int(world[(y+1)%h][(x+1)%w])
	aliveNeighbours /= 255
	return aliveNeighbours
}

// func worker() {

// }

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p golParams, d distributorChans, alive chan []cell) {

	// Create the 2D slice to store the world.
	world := make([][]byte, p.imageHeight)
	for i := range world {
		world[i] = make([]byte, p.imageWidth)
	}

	// Request the io goroutine to read in the image with the given filename.
	d.io.command <- ioInput
	d.io.filename <- strings.Join([]string{strconv.Itoa(p.imageWidth), strconv.Itoa(p.imageHeight)}, "x")

	// The io goroutine sends the requested image byte by byte, in rows.
	for y := 0; y < p.imageHeight; y++ {
		for x := 0; x < p.imageWidth; x++ {
			val := <-d.io.inputVal
			if val != 0 {
				world[y][x] = val
			}
		}
	}

	// Calculate the new state of Game of Life after the given number of turns.
	for turns := 0; turns < p.turns; turns++ {
		var newWorld [][]byte
		for y := 0; y < p.imageHeight; y++ {
			var row []byte
			for x := 0; x < p.imageWidth; x++ {
				aliveNeighbours := countAliveNeighbours(world, x, y, p.imageWidth, p.imageHeight)

				row = append(row, world[y][x])
				if world[y][x] != 0 {
					if !(aliveNeighbours == 2 || aliveNeighbours == 3) {
						row[x] = row[x] ^ 0xFF
					}
				} else if aliveNeighbours == 3 {
					row[x] = row[x] ^ 0xFF
				}
			}
			newWorld = append(newWorld, row)
		}
		world = newWorld
	}

	// Request the io goroutine to write in the image with the given filename.
	d.io.command <- ioOutput
	d.io.filename <- strings.Join([]string{strconv.Itoa(p.imageWidth), strconv.Itoa(p.imageHeight), strconv.Itoa(p.turns)}, "x")

	for y := 0; y < p.imageHeight; y++ {
		for x := 0; x < p.imageWidth; x++ {
			d.io.outputVal <- world[y][x]
		}
	}

	// Create an empty slice to store coordinates of cells that are still alive after p.turns are done.
	var finalAlive []cell
	// Go through the world and append the cells that are still alive.
	for y := 0; y < p.imageHeight; y++ {
		for x := 0; x < p.imageWidth; x++ {
			if world[y][x] != 0 {
				finalAlive = append(finalAlive, cell{x: x, y: y})
			}
		}
	}

	// Make sure that the Io has finished any output before exiting.
	d.io.command <- ioCheckIdle
	<-d.io.idle

	// Return the coordinates of cells that are still alive.
	alive <- finalAlive
}
