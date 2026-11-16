[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$OutputDirectory
)

# Draws the installer's pictures from lgmon's own mark, so the repository carries a script instead of large BMPs.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing

$brand = [Drawing.Color]::FromArgb(255, 0xC9, 0x64, 0x42)
$cream = [Drawing.Color]::FromArgb(255, 0xFA, 0xF9, 0xF5)
$ink = [Drawing.Color]::FromArgb(255, 0x14, 0x14, 0x13)
$muted = [Drawing.Color]::FromArgb(255, 0x73, 0x72, 0x6C)
$up = [Drawing.Color]::FromArgb(255, 0x5C, 0xDD, 0x8B)

function New-RoundedPath([single]$X, [single]$Y, [single]$Width, [single]$Height, [single]$Radius) {
    $path = New-Object Drawing.Drawing2D.GraphicsPath
    $diameter = [Math]::Min($Radius * 2, [Math]::Min($Width, $Height))
    $path.AddArc($X, $Y, $diameter, $diameter, 180, 90)
    $path.AddArc($X + $Width - $diameter, $Y, $diameter, $diameter, 270, 90)
    $path.AddArc($X + $Width - $diameter, $Y + $Height - $diameter, $diameter, $diameter, 0, 90)
    $path.AddArc($X, $Y + $Height - $diameter, $diameter, $diameter, 90, 90)
    $path.CloseFigure()
    return $path
}

function New-Canvas([int]$Width, [int]$Height, [Drawing.Imaging.PixelFormat]$Format) {
    $bitmap = New-Object Drawing.Bitmap $Width, $Height, $Format
    $graphics = [Drawing.Graphics]::FromImage($bitmap)
    $graphics.SmoothingMode = [Drawing.Drawing2D.SmoothingMode]::AntiAlias
    $graphics.InterpolationMode = [Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
    $graphics.TextRenderingHint = [Drawing.Text.TextRenderingHint]::AntiAliasGridFit
    return @($bitmap, $graphics)
}

# The same mark as web/src/brand.tsx: four heartbeat bars on a warm tile, the newest one brightest.
function Draw-Tile($Graphics, [single]$X, [single]$Y, [single]$Size) {
    $scale = $Size / 32
    $Graphics.FillPath((New-Object Drawing.SolidBrush $brand), (New-RoundedPath $X $Y $Size $Size (9 * $scale)))
    $opacities = 0.42, 0.6, 0.8, 1.0
    for ($index = 0; $index -lt 4; $index++) {
        $bar = New-RoundedPath ($X + (7 + $index * 5) * $scale) ($Y + 9.5 * $scale) (3 * $scale) (13 * $scale) (1.5 * $scale)
        $color = [Drawing.Color]::FromArgb([int](255 * $opacities[$index]), $cream)
        $Graphics.FillPath((New-Object Drawing.SolidBrush $color), $bar)
    }
}

function Draw-Glow($Graphics, [single]$CenterX, [single]$CenterY, [single]$Radius, [Drawing.Color]$Color) {
    $path = New-Object Drawing.Drawing2D.GraphicsPath
    $path.AddEllipse($CenterX - $Radius, $CenterY - $Radius, $Radius * 2, $Radius * 2)
    $glow = New-Object Drawing.Drawing2D.PathGradientBrush $path
    $glow.CenterColor = $Color
    $glow.SurroundColors = @([Drawing.Color]::FromArgb(0, $Color))
    $Graphics.FillPath($glow, $path)
}

function Save-Bitmap($Bitmap, [string]$Name) {
    $path = Join-Path $OutputDirectory $Name
    $Bitmap.Save($path, [Drawing.Imaging.ImageFormat]::Bmp)
    return $path
}

New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
$large = @()
foreach ($width in 164, 246, 328) {
    $height = [int][Math]::Round($width * 314 / 164)
    $scale = $width / 164
    $bitmap, $graphics = New-Canvas $width $height ([Drawing.Imaging.PixelFormat]::Format24bppRgb)
    $graphics.Clear($cream)
    Draw-Glow $graphics (10 * $scale) (40 * $scale) (130 * $scale) ([Drawing.Color]::FromArgb(60, $brand))
    Draw-Glow $graphics ($width - 10 * $scale) ($height - 50 * $scale) (140 * $scale) ([Drawing.Color]::FromArgb(55, 120, 140, 190))
    $tile = 60 * $scale
    Draw-Tile $graphics (($width - $tile) / 2) (92 * $scale) $tile
    $center = New-Object Drawing.StringFormat
    $center.Alignment = [Drawing.StringAlignment]::Center
    $wordmark = New-Object Drawing.Font 'Georgia', (20 * $scale), ([Drawing.FontStyle]::Regular), ([Drawing.GraphicsUnit]::Pixel)
    $graphics.DrawString('lgmon', $wordmark, (New-Object Drawing.SolidBrush $ink), (New-Object Drawing.RectangleF 0, (160 * $scale), $width, (30 * $scale)), $center)
    $caption = New-Object Drawing.Font 'Segoe UI', (9.5 * $scale), ([Drawing.FontStyle]::Regular), ([Drawing.GraphicsUnit]::Pixel)
    $graphics.DrawString('Windows service monitor', $caption, (New-Object Drawing.SolidBrush $muted), (New-Object Drawing.RectangleF 0, (188 * $scale), $width, (16 * $scale)), $center)
    # A row of heartbeats, all healthy, like a board that just came up.
    $bars = 14
    $barWidth = 4 * $scale
    $gap = 3.2 * $scale
    $start = ($width - ($bars * $barWidth + ($bars - 1) * $gap)) / 2
    for ($index = 0; $index -lt $bars; $index++) {
        $alpha = [int](90 + 165 * $index / ($bars - 1))
        $bar = New-RoundedPath ($start + $index * ($barWidth + $gap)) (250 * $scale) $barWidth (16 * $scale) (2 * $scale)
        $graphics.FillPath((New-Object Drawing.SolidBrush ([Drawing.Color]::FromArgb($alpha, $up))), $bar)
    }
    $graphics.Dispose()
    $large += Save-Bitmap $bitmap ('wizard-' + $width + '.bmp')
    $bitmap.Dispose()
}

$small = @()
foreach ($size in 55, 83, 110) {
    $bitmap, $graphics = New-Canvas $size $size ([Drawing.Imaging.PixelFormat]::Format24bppRgb)
    $graphics.Clear([Drawing.Color]::White)
    $tile = $size * 0.78
    Draw-Tile $graphics (($size - $tile) / 2) (($size - $tile) / 2) $tile
    $graphics.Dispose()
    $small += Save-Bitmap $bitmap ('wizard-small-' + $size + '.bmp')
    $bitmap.Dispose()
}

# A multi-size icon with PNG entries, which Windows Vista and later read directly.
$images = @()
foreach ($size in 16, 24, 32, 48, 64, 128, 256) {
    $bitmap, $graphics = New-Canvas $size $size ([Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $graphics.Clear([Drawing.Color]::Transparent)
    Draw-Tile $graphics 0 0 $size
    $graphics.Dispose()
    $stream = New-Object IO.MemoryStream
    $bitmap.Save($stream, [Drawing.Imaging.ImageFormat]::Png)
    $bitmap.Dispose()
    $images += [pscustomobject]@{size = $size; data = $stream.ToArray()}
}
$icon = Join-Path $OutputDirectory 'lgmon.ico'
$writer = New-Object IO.BinaryWriter ([IO.File]::Create($icon))
try {
    $writer.Write([uint16]0)
    $writer.Write([uint16]1)
    $writer.Write([uint16]$images.Count)
    $offset = 6 + 16 * $images.Count
    foreach ($image in $images) {
        $dimension = if ($image.size -ge 256) { 0 } else { $image.size }
        $writer.Write([byte]$dimension)
        $writer.Write([byte]$dimension)
        $writer.Write([byte]0)
        $writer.Write([byte]0)
        $writer.Write([uint16]1)
        $writer.Write([uint16]32)
        $writer.Write([uint32]$image.data.Length)
        $writer.Write([uint32]$offset)
        $offset += $image.data.Length
    }
    foreach ($image in $images) { $writer.Write($image.data) }
}
finally { $writer.Dispose() }

[pscustomobject]@{wizardImages = ($large -join ','); wizardSmallImages = ($small -join ','); icon = $icon} | ConvertTo-Json -Compress
