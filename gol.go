package main

import (
	"strconv"
	"strings"
)

func countAliveNeighbours(slice [][]byte, x, y, w, h int) int {
	aliveNeighbours := 0
	aliveNeighbours += int(slice[(y-1+h)%h][(x-1+w)%w])
	aliveNeighbours += int(slice[(y-1+h)%h][x])
	aliveNeighbours += int(slice[(y-1+h)%h][(x+1)%w])
	aliveNeighbours += int(slice[y][(x-1+w)%w])
	aliveNeighbours += int(slice[y][(x+1)%w])
	aliveNeighbours += int(slice[(y+1)%h][(x-1+w)%w])
	aliveNeighbours += int(slice[(y+1)%h][x])
	aliveNeighbours += int(slice[(y+1)%h][(x+1)%w])
	aliveNeighbours /= 255
	return aliveNeighbours
}

func receiveRow(width int, val chan byte) []byte {
	row := make([]byte, width)
	for x := 0; x < width; x++ {
		row[x] = <-val
	}
	return row
}

func worker(p golParams, val chan byte) {
	sliceHeight := (p.imageHeight / p.threads) + 2
	workerSlice := make([][]byte, sliceHeight)
	for i := range workerSlice {
		workerSlice[i] = make([]byte, p.imageWidth)
	}

	for i := 0; i < p.turns; i++ {
		// Receive top row
		workerSlice[0] = receiveRow(p.imageWidth, val)
		// Receive center section if first turn
		if i == 0 {
			for j := 1; j < sliceHeight-1; j++ {
				workerSlice[j] = receiveRow(p.imageWidth, val)
			}
		}
		// Receive bottom row
		workerSlice[sliceHeight-1] = receiveRow(p.imageWidth, val)

		// Create temporary slice
		newSlice := make([][]byte, sliceHeight)
		for i := range newSlice {
			newSlice[i] = make([]byte, p.imageWidth)
		}

		// Process center and update workerSlice
		for y := 1; y < sliceHeight-1; y++ {
			row := make([]byte, p.imageWidth)
			for x := 0; x < p.imageWidth; x++ {
				aliveNeighbours := countAliveNeighbours(workerSlice, x, y, p.imageWidth, sliceHeight)

				row[x] = workerSlice[y][x]
				if workerSlice[y][x] != 0 {
					if !(aliveNeighbours == 2 || aliveNeighbours == 3) {
						row[x] = 0x00
					}
				} else if aliveNeighbours == 3 {
					row[x] = 0xFF
				}
			}
			newSlice[y] = row
		}
		workerSlice = newSlice

		// Send center to distributor
		for y := 1; y < sliceHeight-1; y++ {
			for x := 0; x < p.imageWidth; x++ {
				val <- workerSlice[y][x]
			}
		}
	}
}

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

	// Create worker channels and initialise worker threads
	workerHeight := p.imageHeight / p.threads
	workerChannels := make([]chan byte, p.threads)
	for i := 0; i < p.threads; i++ {
		workerChannels[i] = make(chan byte)
		go worker(p, workerChannels[i])
	}

	// Calculate the new state of Game of Life after the given number of turns.
	for turn := 0; turn < p.turns; turn++ {
		// send rows to workers
		for i := 0; i < p.threads; i++ {
			// Send top row
			y := ((i * workerHeight) - 1 + p.imageHeight) % p.imageHeight
			for x := 0; x < p.imageWidth; x++ {
				workerChannels[i] <- world[y][x]
			}
			// Send center rows if turn 0
			if turn == 0 {
				for y := i * workerHeight; y < (i+1)*workerHeight; y++ {
					for x := 0; x < p.imageWidth; x++ {
						workerChannels[i] <- world[y][x]
					}
				}
			}
			// Send bottom row
			y = ((i + 1) * workerHeight) % p.imageHeight
			for x := 0; x < p.imageWidth; x++ {
				workerChannels[i] <- world[y][x]
			}
		}

		// Create new temporary 2d slice
		newWorld := make([][]byte, p.imageHeight)
		for i := range newWorld {
			newWorld[i] = make([]byte, p.imageWidth)
		}

		// Recieve rows from workers
		for i := 0; i < p.threads; i++ {
			for j := i * workerHeight; j < (i+1)*workerHeight; j++ {
				newWorld[j] = receiveRow(p.imageWidth, workerChannels[i])
			}
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
