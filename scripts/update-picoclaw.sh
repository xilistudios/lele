#!/bin/bash
#
# Script de actualización de Picoclaw
# Actualiza el binario desde el código fuente y reinicia el servicio
#

set -euo pipefail

# Configuración
PROJECT_DIR="/home/alfredo/.picoclaw/workspace-coder/picoclaw"
BINARY_NAME="picoclaw"
INSTALL_DIR="/usr/local/bin"
SERVICE_NAME="picoclaw"
BACKUP_DIR="/home/alfredo/.picoclaw/backups"
LOG_FILE="/var/log/picoclaw-update.log"

# Colores para output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Funciones de utilidad
log() {
    echo -e "${BLUE}[$(date '+%Y-%m-%d %H:%M:%S')]${NC} $1" | tee -a "$LOG_FILE"
}

log_success() {
    echo -e "${GREEN}[✓]${NC} $1" | tee -a "$LOG_FILE"
}

log_warning() {
    echo -e "${YELLOW}[!]${NC} $1" | tee -a "$LOG_FILE"
}

log_error() {
    echo -e "${RED}[✗]${NC} $1" | tee -a "$LOG_FILE"
}

error_exit() {
    log_error "$1"
    exit 1
}

# Verificar si se ejecuta como root o con sudo
check_permissions() {
    if [[ $EUID -ne 0 ]]; then
        log_error "Este script debe ejecutarse como root o con sudo"
        exit 1
    fi
}

# Configurar PATH para incluir Go
setup_go_path() {
    if [[ -d "/usr/local/go/bin" ]]; then
        export PATH="$PATH:/usr/local/go/bin"
    fi
}

# Verificar dependencias
check_dependencies() {
    log "Verificando dependencias..."
    
    command -v git >/dev/null 2>&1 || error_exit "git no está instalado"
    command -v go >/dev/null 2>&1 || error_exit "Go no está instalado (buscado en PATH y /usr/local/go/bin)"
    command -v systemctl >/dev/null 2>&1 || error_exit "systemctl no está disponible"
    
    local go_version
    go_version=$(go version 2>&1)
    log_success "Todas las dependencias están instaladas ($go_version)"
}

# Crear directorios necesarios
setup_directories() {
    log "Configurando directorios..."
    mkdir -p "$BACKUP_DIR"
    touch "$LOG_FILE"
    log_success "Directorios configurados"
}

# Hacer backup del binario actual
backup_current() {
    log "Creando backup del binario actual..."
    
    if [[ -f "$INSTALL_DIR/$BINARY_NAME" ]]; then
        local backup_name="${BINARY_NAME}-$(date +%Y%m%d-%H%M%S).bak"
        cp "$INSTALL_DIR/$BINARY_NAME" "$BACKUP_DIR/$backup_name"
        log_success "Backup creado: $BACKUP_DIR/$backup_name"
    else
        log_warning "No se encontró binario actual para hacer backup"
    fi
}

# Actualizar código fuente desde git
update_source() {
    log "Actualizando código fuente..."
    
    cd "$PROJECT_DIR" || error_exit "No se pudo acceder a $PROJECT_DIR"
    
    # Configurar git para evitar verificación de host (solo para fetch)
    export GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=no'
    
    # Guardar el commit actual para el log
    local old_commit
    old_commit=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
    
    # Hacer pull de los cambios
    git fetch origin 2>/dev/null || {
        log_warning "No se pudo conectar al repositorio remoto (posiblemente sin internet o repo privado)"
        log "Continuando con recompilación local..."
    }
    
    local new_commit
    new_commit=$(git rev-parse --short origin/HEAD 2>/dev/null || echo "$old_commit")
    
    if [[ "$old_commit" == "$new_commit" ]]; then
        log_warning "El código ya está actualizado (commit: $old_commit)"
        read -p "¿Deseas recompilar de todos modos? (y/N): " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            log "Actualización cancelada por el usuario"
            exit 0
        fi
    else
        git pull || error_exit "No se pudo hacer pull del repositorio"
        log_success "Código actualizado: $old_commit → $new_commit"
    fi
}

# Compilar el binario
build_binary() {
    log "Compilando Picoclaw..."
    
    cd "$PROJECT_DIR" || error_exit "No se pudo acceder a $PROJECT_DIR"
    
    # Limpiar builds anteriores
    rm -f "$BINARY_NAME"
    
    # Compilar
    export PATH=$PATH:/usr/local/go/bin
    go build -o "$BINARY_NAME" ./cmd/picoclaw || error_exit "Error al compilar"
    
    # Verificar que se creó el binario
    if [[ ! -f "$BINARY_NAME" ]]; then
        error_exit "El binario no se creó correctamente"
    fi
    
    local version
    version=$(./$BINARY_NAME version 2>&1 | head -1)
    log_success "Compilación exitosa: $version"
}

# Instalar el nuevo binario
install_binary() {
    log "Instalando nuevo binario..."
    
    # Detener el servicio antes de actualizar
    log "Deteniendo servicio $SERVICE_NAME..."
    systemctl stop "$SERVICE_NAME" || log_warning "No se pudo detener el servicio (podría no estar corriendo)"
    
    # Copiar el nuevo binario
    cp "$PROJECT_DIR/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
    chmod +x "$INSTALL_DIR/$BINARY_NAME"
    
    log_success "Binario instalado en $INSTALL_DIR/$BINARY_NAME"
}

# Iniciar el servicio
start_service() {
    log "Iniciando servicio $SERVICE_NAME..."
    
    systemctl start "$SERVICE_NAME" || error_exit "No se pudo iniciar el servicio"
    
    # Esperar un momento para que el servicio se inicie
    sleep 2
    
    # Verificar estado
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        log_success "Servicio $SERVICE_NAME iniciado correctamente"
    else
        error_exit "El servicio no se inició correctamente"
    fi
}

# Verificar la instalación
verify_installation() {
    log "Verificando instalación..."
    
    local version
    version=$("$INSTALL_DIR/$BINARY_NAME" version 2>&1)
    
    log_success "Picoclaw instalado correctamente:"
    echo "$version" | tee -a "$LOG_FILE"
    
    # Verificar que el servicio está respondiendo
    log "Verificando estado del servicio..."
    sleep 2
    
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        log_success "Servicio activo y corriendo"
    else
        log_error "El servicio no está corriendo"
        return 1
    fi
}

# Limpiar archivos temporales
cleanup() {
    log "Limpiando archivos temporales..."
    rm -f "$PROJECT_DIR/$BINARY_NAME"
    
    # Mantener solo los últimos 5 backups
    local backup_count
    backup_count=$(ls -1 "$BACKUP_DIR"/*.bak 2>/dev/null | wc -l)
    if [[ $backup_count -gt 5 ]]; then
        ls -t "$BACKUP_DIR"/*.bak | tail -n +6 | xargs -r rm
        log_success "Backups antiguos eliminados (manteniendo últimos 5)"
    fi
}

# Función para rollback en caso de error
rollback() {
    log_error "Ocurrió un error. Intentando rollback..."
    
    local latest_backup
    latest_backup=$(ls -t "$BACKUP_DIR"/*.bak 2>/dev/null | head -1)
    
    if [[ -n "$latest_backup" && -f "$latest_backup" ]]; then
        log "Restaurando backup: $latest_backup"
        systemctl stop "$SERVICE_NAME" 2>/dev/null || true
        cp "$latest_backup" "$INSTALL_DIR/$BINARY_NAME"
        systemctl start "$SERVICE_NAME"
        log_success "Rollback completado"
    else
        log_error "No se encontró backup para restaurar"
    fi
    
    exit 1
}

# Mostrar uso del script
show_usage() {
    echo "Uso: $0 [opciones]"
    echo ""
    echo "Opciones:"
    echo "  -h, --help      Muestra esta ayuda"
    echo "  -b, --build     Solo compila, no actualiza el servicio"
    echo "  -s, --status    Muestra el estado actual del servicio"
    echo "  -r, --restore   Restaura el último backup"
    echo ""
    echo "Ejemplos:"
    echo "  sudo $0           # Actualización completa"
    echo "  sudo $0 --build   # Solo compilar"
    echo "  sudo $0 --status  # Ver estado"
}

# Mostrar estado del servicio
show_status() {
    echo "=== Estado de Picoclaw ==="
    echo ""
    systemctl status "$SERVICE_NAME" --no-pager
    echo ""
    echo "=== Versión instalada ==="
    "$INSTALL_DIR/$BINARY_NAME" version 2>&1
    echo ""
    echo "=== Backups disponibles ==="
    ls -lh "$BACKUP_DIR"/*.bak 2>/dev/null || echo "No hay backups"
    echo ""
    echo "=== Última actualización ==="
    tail -5 "$LOG_FILE" 2>/dev/null || echo "No hay logs"
}

# Restaurar desde backup
restore_backup() {
    local latest_backup
    latest_backup=$(ls -t "$BACKUP_DIR"/*.bak 2>/dev/null | head -1)
    
    if [[ -z "$latest_backup" || ! -f "$latest_backup" ]]; then
        error_exit "No se encontró backup para restaurar"
    fi
    
    log "Restaurando desde: $latest_backup"
    
    systemctl stop "$SERVICE_NAME" || true
    cp "$latest_backup" "$INSTALL_DIR/$BINARY_NAME"
    systemctl start "$SERVICE_NAME"
    
    log_success "Restauración completada"
    show_status
}

# === MAIN ===

main() {
    # Manejar argumentos
    case "${1:-}" in
        -h|--help)
            show_usage
            exit 0
            ;;
        -s|--status)
            show_status
            exit 0
            ;;
        -b|--build)
            setup_go_path
            check_dependencies
            update_source
            build_binary
            log_success "Compilación completada. Binario en: $PROJECT_DIR/$BINARY_NAME"
            exit 0
            ;;
        -r|--restore)
            check_permissions
            restore_backup
            exit 0
            ;;
    esac
    
    # Configurar trap para rollback en caso de error
    trap rollback ERR
    
    echo "=========================================="
    echo "    Actualización de Picoclaw 🦞"
    echo "=========================================="
    echo ""
    
    log "Iniciando proceso de actualización..."
    
    # Configurar PATH y verificaciones iniciales
    setup_go_path
    check_permissions
    check_dependencies
    setup_directories
    
    # Proceso de actualización
    backup_current
    update_source
    build_binary
    install_binary
    start_service
    verify_installation
    cleanup
    
    echo ""
    echo "=========================================="
    log_success "¡Actualización completada exitosamente! 🎉"
    echo "=========================================="
    echo ""
    echo "Logs disponibles en: $LOG_FILE"
    echo "Backups guardados en: $BACKUP_DIR"
}

main "$@"
